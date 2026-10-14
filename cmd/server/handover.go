package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// handover lists leavers, or moves one leaver's My files to a shared workspace or to the
// active directory account with the exact subject. It prints IDs and counts, never file names.
func handover(ctx context.Context, st store.Store, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("handover", flag.ContinueOnError)
	from := fs.String("from", "", "leaver's personal workspace ID (omit to list leavers)")
	workspace := fs.String("workspace", "", "destination shared workspace ID")
	subject := fs.String("to-user", "", "destination colleague's exact KyIdentity OIDC subject / SCIM externalId")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		leavers, err := st.Drive().OperatorLeavers(ctx)
		for _, l := range leavers {
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%d bytes\t%d items\n", l.Workspace, l.Status, l.Name, l.Size, l.Items)
		}
		return err
	}
	if (*workspace == "") == (*subject == "") {
		return errors.New("handover requires -from and exactly one of -workspace, -to-user")
	}
	to := drive.Target{Workspace: *workspace}
	if *subject != "" {
		var found []*store.User
		for _, provider := range []string{"scim", "kysignon"} {
			if u, err := st.Users().GetUserBySSO(ctx, provider, *subject); err == nil && u.Status == "active" {
				found = append(found, u)
			} else if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
		}
		if len(found) != 1 {
			return fmt.Errorf("expected exactly one active directory account; found %d", len(found))
		}
		dedicated, err := st.InstallerAdministrator(ctx, found[0].ID)
		if err != nil {
			return err
		}
		if dedicated {
			return errors.New("dedicated installer administrators hold no personal files")
		}
		to.User = found[0].ID
	}
	folder, err := st.Drive().OperatorHandover(ctx, *from, to)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Handed over %s into folder %s\n", *from, folder)
	return nil
}
