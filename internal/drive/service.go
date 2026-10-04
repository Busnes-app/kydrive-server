package drive

import (
	"context"
	"database/sql"
	"github.com/google/uuid"
	"time"
)

type ServiceAccess struct {
	Workspace string
	Rank      int
}
type serviceAccessKey struct{}

func WithServiceAccess(ctx context.Context, a ServiceAccess) context.Context {
	return context.WithValue(ctx, serviceAccessKey{}, a)
}
func (s *Store) CreateServiceToken(ctx context.Context, user, workspace, name, role, token string) (string, error) {
	if !ValidName(name) || Rank(role) < 1 || Rank(role) > 2 {
		return "", ErrInvalid
	}
	id := uuid.NewString()
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := authorize(ctx, tx, user, workspace, 3); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO drive_service_tokens(id,user_id,workspace,name,role,token_hash,expires) VALUES(?,?,?,?,?,?,?)`, id, user, workspace, name, role, tokenHash(token), time.Now().Add(90*24*time.Hour).Unix())
		if err != nil {
			return err
		}
		return event(ctx, tx, user, "service.token_created", id)
	})
	return id, err
}
func (s *Store) ServiceToken(ctx context.Context, token string) (string, ServiceAccess, error) {
	var user, role string
	var a ServiceAccess
	err := s.db.QueryRowContext(ctx, `SELECT user_id,workspace,role FROM drive_service_tokens WHERE token_hash=? AND expires>? AND revoked=false`, tokenHash(token), time.Now().Unix()).Scan(&user, &a.Workspace, &role)
	if err != nil {
		return "", a, ErrDenied
	}
	a.Rank = Rank(role)
	if err = authorize(ctx, s.db, user, a.Workspace, a.Rank); err != nil {
		return "", a, err
	}
	return user, a, nil
}
func (s *Store) RevokeServiceToken(ctx context.Context, user, id string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var workspace string
		if err := tx.QueryRowContext(ctx, `SELECT workspace FROM drive_service_tokens WHERE id=?`, id).Scan(&workspace); err != nil {
			return ErrInvalid
		}
		if err := authorize(ctx, tx, user, workspace, 3); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE drive_service_tokens SET revoked=true WHERE id=?`, id)
		if err != nil {
			return err
		}
		return event(ctx, tx, user, "service.token_revoked", id)
	})
}
