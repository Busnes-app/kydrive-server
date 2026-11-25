package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// storageSeq lists the orgs whose storage reports KyIdentity answered stale (after a restore),
// or, with -org and -past, moves one org's report sequence to past, at least the sequence
// KyIdentity holds, so its next report applies (KyIdentity "After a restore").
func storageSeq(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("storage-seq", flag.ContinueOnError)
	org := fs.String("org", "", "org ID to advance (omit to list stale orgs)")
	past := fs.Int64("past", 0, "new report sequence, at least the one KyIdentity holds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || (*org == "") != (*past == 0) {
		return errors.New("storage-seq takes no arguments, or -org with -past")
	}
	cfg, err := config.LoadExisting()
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.Database, cfg.Security.EncryptionKey)
	if err != nil {
		return err
	}
	defer st.Close()
	return storageSeqOn(ctx, st, *org, *past, stdout)
}

func storageSeqOn(ctx context.Context, st store.Store, org string, past int64, stdout io.Writer) error {
	d := st.Drive()
	if d == nil || !d.Multi() {
		return errors.New("storage reports exist only on a multi-tenant install")
	}
	if org == "" {
		stale, err := d.StaleReports(ctx)
		for _, r := range stale {
			fmt.Fprintf(stdout, "%s\tsequence %d\tKyIdentity holds %d\n", r.Org, r.Seq, r.Stale)
		}
		return err
	}
	if err := d.AdvanceReportSeq(ctx, org, past); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Org %s reports from sequence %d; its grant is asked for again\n", org, past)
	return nil
}
