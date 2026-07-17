package secrets

import (
	"errors"
	"path/filepath"
	"testing"
)

func fixedPassword(pw string) func() (string, error) {
	return func() (string, error) { return pw, nil }
}

func TestKeepassDriverRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.kdbx")
	const pw = "correct horse battery staple"

	// First store: file does not exist yet, so Unlock creates it.
	store := NewStore(&KeepassDriver{Path: path, PromptPassword: fixedPassword(pw)})

	if err := store.SetValue("api/token", "s3cr3t"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	if err := store.SetValue("db/password", "hunter2"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	// Update an existing key; CreatedAt must be preserved, value replaced.
	before, err := store.Get("api/token")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := store.SetValue("api/token", "rotated"); err != nil {
		t.Fatalf("SetValue (update): %v", err)
	}
	after, err := store.Get("api/token")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Value != "rotated" {
		t.Fatalf("value not updated: got %q", after.Value)
	}
	if before.CreatedAt == nil || after.CreatedAt == nil || !after.CreatedAt.Equal(*before.CreatedAt) {
		t.Fatalf("CreatedAt not preserved across update: before=%v after=%v", before.CreatedAt, after.CreatedAt)
	}

	// Reopen from disk with a fresh driver to prove persistence + decryption.
	reopened := NewStore(&KeepassDriver{Path: path, PromptPassword: fixedPassword(pw)})
	v, err := reopened.GetValue("db/password")
	if err != nil {
		t.Fatalf("GetValue after reopen: %v", err)
	}
	if *v != "hunter2" {
		t.Fatalf("wrong value after reopen: got %q want %q", *v, "hunter2")
	}

	secrets, err := reopened.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(secrets) != 2 {
		t.Fatalf("List returned %d secrets, want 2", len(secrets))
	}

	// Delete then confirm ErrNotFound.
	if err := reopened.Delete("db/password"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := reopened.Get("db/password"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestKeepassDriverReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.kdbx")
	const pw = "open sesame"

	// Populate the database in read-write mode.
	rw := NewStore(&KeepassDriver{Path: path, PromptPassword: fixedPassword(pw)})
	if err := rw.SetValue("k", "v"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	// Reopen read-only.
	ro := &KeepassDriver{Path: path, PromptPassword: fixedPassword(pw), ReadOnly: true}
	store := NewStore(ro)

	v, err := store.GetValue("k")
	if err != nil {
		t.Fatalf("GetValue (read-only): %v", err)
	}
	if *v != "v" {
		t.Fatalf("read-only value = %q, want %q", *v, "v")
	}

	if err := store.SetValue("k", "changed"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("SetValue on read-only driver err = %v, want ErrReadOnly", err)
	}
	if err := store.Delete("k"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Delete on read-only driver err = %v, want ErrReadOnly", err)
	}

	// The master credentials must have been dropped after unlock.
	if ro.db.Credentials != nil {
		t.Fatal("expected credentials to be dropped in read-only mode")
	}
}

func TestKeepassDriverReadOnlyMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.kdbx")
	ro := NewStore(&KeepassDriver{Path: path, PromptPassword: fixedPassword("x"), ReadOnly: true})
	if _, err := ro.Get("k"); err == nil {
		t.Fatal("expected error opening missing file read-only, got nil")
	}
}

func TestKeepassDriverWrongPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.kdbx")

	create := NewStore(&KeepassDriver{Path: path, PromptPassword: fixedPassword("right")})
	if err := create.SetValue("k", "v"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	wrong := NewStore(&KeepassDriver{Path: path, PromptPassword: fixedPassword("wrong")})
	if _, err := wrong.Get("k"); err == nil {
		t.Fatal("expected error opening with wrong password, got nil")
	}
}
