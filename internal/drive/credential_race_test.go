package drive_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// A password change that commits while an app password is being made through the old sign-in
// leaves no usable app password: the creation reruns, finds the sign-in gone and is refused.
func TestPasswordChangeEndsAnAppPasswordBeingCreated(t *testing.T) {
	if !testdb.Postgres() {
		t.Skip("SQLite has one writer: the change waits for the creation to commit")
	}
	st, _, _ := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	if err := drive.ExecSQL(ctx, d, `UPDATE users SET sso_provider='local',password_hash='old',must_change_password=true WHERE id='worker'`); err != nil {
		t.Fatal(err)
	}
	if err := drive.ExecSQL(ctx, d, `INSERT INTO sessions(token_hash,user_id,user_agent,ip_address,created_at,expires_at,sso_sid) VALUES('h','worker','','',?,?,'')`, time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	login := drive.WithLogin(ctx, drive.Login{TokenHash: "h", At: time.Now()})
	paused, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer drive.PauseCredential(func() { once.Do(func() { close(paused); <-resume }) })()
	type made struct {
		secret string
		err    error
	}
	done := make(chan made, 1)
	go func() {
		_, secret, err := d.CreateAppPassword(login, "worker", "laptop")
		done <- made{secret, err}
	}()
	<-paused
	if err := st.Users().CompletePasswordChange(ctx, "worker", "old", "new", ""); err != nil {
		t.Fatal(err)
	}
	close(resume)
	m := <-done
	if m.err != nil {
		return
	}
	if _, err := d.AppPasswordUser(ctx, "worker", m.secret); err == nil {
		t.Fatal("an app password made through the old sign-in survived the password change")
	}
}
