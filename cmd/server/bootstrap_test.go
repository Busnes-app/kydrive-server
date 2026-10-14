package main

import (
	"os"
	"testing"
)

func TestInitialAdminPasswordFileIsPrivateAndWriteOnce(t *testing.T) {
	dir := t.TempDir()
	path, err := writeInitialAdminPassword(dir, "generated-secret")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, err %v", info.Mode(), err)
	}
	if b, _ := os.ReadFile(path); string(b) != "generated-secret\n" {
		t.Fatalf("content %q", b)
	}
	if _, err := writeInitialAdminPassword(dir, "second"); err == nil {
		t.Fatal("overwrote an existing password file")
	}
	if b, _ := os.ReadFile(path); string(b) != "generated-secret\n" {
		t.Fatal("existing password file changed")
	}
}
