package vault_test

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"filippo.io/age"
	"github.com/masonhuemmer/jkins/internal/vault"
)

func TestCredentialRoundTripIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "jkins")
	store := vault.Store{Dir: dir}
	want := vault.Credential{User: "alice@example.test", Token: "unique-secret-token"}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, present, err := store.Load()
	if err != nil || !present || got != want {
		t.Fatalf("Load() = %+v, %v, %v", got, present, err)
	}
	for _, path := range []string{dir, filepath.Join(dir, "keys")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0700 {
			t.Errorf("%s mode = %o", path, info.Mode().Perm())
		}
	}
	for _, path := range []string{filepath.Join(dir, "vault.json"), filepath.Join(dir, "keys", "identity.txt")} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(want.User)) || bytes.Contains(data, []byte(want.Token)) {
			t.Errorf("%s contains credential plaintext", path)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("%s mode = %o", path, info.Mode().Perm())
		}
	}
	if err := store.Delete(); err != nil {
		t.Fatal(err)
	}
	_, present, err = store.Load()
	if err != nil || present {
		t.Fatalf("Load after delete: present=%v err=%v", present, err)
	}
}

func TestCorruptVaultCannotBeOverwrittenOrDeleted(t *testing.T) {
	for _, corrupt := range []string{"vault", "identity", "missing identity", "wrong identity", "ciphertext"} {
		t.Run(corrupt, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "jkins")
			store := vault.Store{Dir: dir}
			if err := store.Save(vault.Credential{User: "alice", Token: "secret"}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "vault.json")
			if corrupt == "identity" || corrupt == "missing identity" || corrupt == "wrong identity" {
				path = filepath.Join(dir, "keys", "identity.txt")
			}
			bad := []byte("corrupt data")
			if corrupt == "wrong identity" {
				identity, err := age.GenerateX25519Identity()
				if err != nil {
					t.Fatal(err)
				}
				bad = []byte(identity.String() + "\n")
			}
			if corrupt == "ciphertext" {
				bad = []byte(`{"version":1,"ciphertext":"bm90LWFnZQ=="}`)
			}
			if corrupt == "missing identity" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, bad, 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.Load(); err == nil {
				t.Fatal("corrupt store loaded")
			}
			if err := store.Save(vault.Credential{User: "bob", Token: "other"}); err == nil {
				t.Fatal("save overwrote corrupt store")
			}
			if err := store.Delete(); err == nil {
				t.Fatal("delete overwrote corrupt store")
			}
			if corrupt == "missing identity" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("missing identity recreated: %v", err)
				}
			} else {
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, bad) {
					t.Fatalf("corrupt bytes changed: %q", got)
				}
			}
		})
	}
}

func TestConcurrentFirstSavesLeaveDecryptableVault(t *testing.T) {
	for attempt := 0; attempt < 5; attempt++ {
		store := vault.Store{Dir: filepath.Join(t.TempDir(), "jkins")}
		start := make(chan struct{})
		var group sync.WaitGroup
		errors := make(chan error, 32)
		for worker := 0; worker < 32; worker++ {
			group.Add(1)
			go func() {
				defer group.Done()
				<-start
				errors <- store.Save(vault.Credential{User: "alice", Token: "secret"})
			}()
		}
		close(start)
		group.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatalf("concurrent save %d: %v", attempt, err)
			}
		}
		credential, present, err := store.Load()
		if err != nil || !present || credential.User != "alice" || credential.Token != "secret" {
			t.Fatalf("load after concurrent saves %d: %+v, %v, %v", attempt, credential, present, err)
		}
	}
}
