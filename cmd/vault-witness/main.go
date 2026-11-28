// vault-witness serves fresh attestations of a KyDrive vault log from independent storage.
package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Busnes-app/ky-primitives/hybridsig"
	"github.com/Busnes-app/kydrive-server/internal/vault"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("vault-witness", flag.ContinueOnError)
	address := flags.String("listen", "127.0.0.1:9443", "HTTPS listen address")
	state := flags.String("state-dir", "", "existing owner-only state directory")
	keyPath := flags.String("key-file", "", "owner-only signing seed file")
	publisher := flags.String("publisher-key", "", "base64 KyDrive receipt verification key")
	cert := flags.String("tls-cert", "", "TLS certificate chain file")
	tlsKey := flags.String("tls-key", "", "TLS private key file")
	initialize := flags.Bool("initialize", false, "initialize a new witness database and exit")
	generate := flags.Bool("generate-key", false, "create a new signing seed file and print its public key")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *keyPath == "" {
		return errors.New("a signing -key-file is required")
	}
	if *generate {
		if *initialize {
			return errors.New("generate-key and initialize are separate operations")
		}
		return generateKey(*keyPath)
	}
	if *state == "" || *publisher == "" {
		return errors.New("-state-dir and -publisher-key are required")
	}
	if !*initialize && (*cert == "" || *tlsKey == "") {
		return errors.New("serving requires -tls-cert and -tls-key")
	}
	info, err := os.Lstat(*state)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("state directory must be owner-only")
	}
	key, err := vault.ReadSigningKey(*keyPath)
	if err != nil {
		return fmt.Errorf("witness signing key: %w", err)
	}
	pub, err := vault.ParsePublicKey(*publisher)
	if err != nil {
		return fmt.Errorf("receipt verification key: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dbPath := filepath.Join(*state, "witness.db")
	if *initialize {
		witness, err := vault.InitializeWitness(ctx, dbPath, key, pub)
		if err != nil {
			return err
		}
		return witness.Close()
	}
	witness, err := vault.OpenWitness(ctx, dbPath, key, pub)
	if err != nil {
		return err
	}
	defer witness.Close()
	server := &http.Server{
		Addr: *address, Handler: witness.Handler(), MaxHeaderBytes: 16 << 10,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13},
	}
	served := make(chan error, 1)
	go func() { served <- server.ListenAndServeTLS(*cert, *tlsKey) }()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}

func generateKey(path string) error {
	key, err := hybridsig.Generate()
	if err != nil {
		return err
	}
	mldsaSeed, edSeed := key.Seeds()
	defer clear(mldsaSeed)
	defer clear(edSeed)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(mldsaSeed, edSeed...))
	if err == nil {
		err = f.Sync()
	}
	if err := errors.Join(err, f.Close()); err != nil {
		_ = os.Remove(path)
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, base64.StdEncoding.EncodeToString(key.Public().Bytes()))
	return err
}
