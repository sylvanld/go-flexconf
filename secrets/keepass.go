package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tobischo/gokeepasslib/v3"
	"github.com/tobischo/gokeepasslib/v3/wrappers"
	"golang.org/x/term"
)

// errLocked is returned by driver methods used before Unlock succeeds.
var errLocked = errors.New("keepass: database is locked; call Unlock first")

// keepassGroupName is the name of the group new databases and new entries are
// created under.
const keepassGroupName = "flexconf"

// KeepassDriver is a Driver backed by a password-protected KeePass (.kdbx) file.
//
// Secrets are mapped to KeePass entries: the secret key is the entry Title, the
// value is the (protected) Password, and the timestamps map to the entry's
// creation / last-modification times. If the file does not exist yet, Unlock
// creates a new empty database protected by the entered password.
type KeepassDriver struct {
	// Path is the location of the .kdbx file.
	Path string

	// PromptPassword, when set, is used by Unlock to obtain the database
	// password. When nil, the password is read from the controlling terminal
	// without echo.
	PromptPassword func() (string, error)

	// ReadOnly opens the database for reading only. After a successful Unlock the
	// master credentials are dropped from memory (so the master key is no longer
	// held), and Set/Delete return ErrReadOnly. A read-only driver will not
	// create a missing database.
	ReadOnly bool

	db     *gokeepasslib.Database
	loaded bool
}

// NewKeepassDriver returns a KeepassDriver for the database at path.
func NewKeepassDriver(path string) *KeepassDriver {
	return &KeepassDriver{Path: path}
}

// Unlock prompts for the database password and opens the .kdbx file, decrypting
// its entries into memory. If the file does not exist, a new empty database is
// created and written with the entered password. Unlock is idempotent.
func (d *KeepassDriver) Unlock() error {
	if d.loaded {
		return nil
	}
	if d.Path == "" {
		return errors.New("keepass: no database path configured")
	}

	password, err := d.promptPassword()
	if err != nil {
		return err
	}

	switch _, statErr := os.Stat(d.Path); {
	case statErr == nil:
		if err := d.open(password); err != nil {
			return err
		}
		if d.ReadOnly {
			// Drop the master credentials: the already-decrypted entries stay
			// readable, but the master key is no longer held in memory and the
			// database can no longer be re-encrypted (writes are rejected).
			d.db.Credentials = nil
		}
	case errors.Is(statErr, os.ErrNotExist):
		if d.ReadOnly {
			return fmt.Errorf("keepass: database %s does not exist", d.Path)
		}
		if err := d.create(password); err != nil {
			return err
		}
	default:
		return statErr
	}

	d.loaded = true
	return nil
}

// Get returns the secret stored under key, or ErrNotFound.
func (d *KeepassDriver) Get(key string) (*Secret, error) {
	if !d.loaded {
		return nil, errLocked
	}
	g, idx := locateEntry(d.db.Content.Root.Groups, key)
	if g == nil {
		return nil, ErrNotFound
	}
	return entryToSecret(&g.Entries[idx]), nil
}

// Set creates or replaces the secret. An existing entry keeps its UUID.
func (d *KeepassDriver) Set(secret Secret) error {
	if !d.loaded {
		return errLocked
	}
	if d.ReadOnly {
		return ErrReadOnly
	}

	entry := buildEntry(secret)
	if g, idx := locateEntry(d.db.Content.Root.Groups, secret.Key); g != nil {
		entry.UUID = g.Entries[idx].UUID
		g.Entries[idx] = entry
	} else {
		root := &d.db.Content.Root.Groups[0]
		root.Entries = append(root.Entries, entry)
	}
	return d.save()
}

// List returns every secret across all groups in the database.
func (d *KeepassDriver) List() ([]Secret, error) {
	if !d.loaded {
		return nil, errLocked
	}

	var out []Secret
	var walk func(groups []gokeepasslib.Group)
	walk = func(groups []gokeepasslib.Group) {
		for i := range groups {
			for j := range groups[i].Entries {
				out = append(out, *entryToSecret(&groups[i].Entries[j]))
			}
			walk(groups[i].Groups)
		}
	}
	walk(d.db.Content.Root.Groups)
	return out, nil
}

// Delete removes the secret stored under key, or returns ErrNotFound.
func (d *KeepassDriver) Delete(key string) error {
	if !d.loaded {
		return errLocked
	}
	if d.ReadOnly {
		return ErrReadOnly
	}
	g, idx := locateEntry(d.db.Content.Root.Groups, key)
	if g == nil {
		return ErrNotFound
	}
	g.Entries = append(g.Entries[:idx], g.Entries[idx+1:]...)
	return d.save()
}

// open decodes the existing database at d.Path using password.
func (d *KeepassDriver) open(password string) error {
	f, err := os.Open(d.Path)
	if err != nil {
		return err
	}
	defer f.Close()

	db := gokeepasslib.NewDatabase()
	db.Credentials = gokeepasslib.NewPasswordCredentials(password)
	if err := gokeepasslib.NewDecoder(f).Decode(db); err != nil {
		return fmt.Errorf("keepass: unable to open database (wrong password?): %w", err)
	}
	if err := db.UnlockProtectedEntries(); err != nil {
		return err
	}
	d.db = db
	return nil
}

// create builds a fresh empty database and writes it to d.Path.
func (d *KeepassDriver) create(password string) error {
	group := gokeepasslib.NewGroup()
	group.Name = keepassGroupName

	db := gokeepasslib.NewDatabase()
	db.Content.Root.Groups = []gokeepasslib.Group{group}
	db.Credentials = gokeepasslib.NewPasswordCredentials(password)

	d.db = db
	return d.save()
}

// save encrypts protected values and writes the database atomically, then
// re-unlocks the in-memory copy so the driver stays usable.
func (d *KeepassDriver) save() error {
	if err := d.db.LockProtectedEntries(); err != nil {
		return err
	}
	defer func() { _ = d.db.UnlockProtectedEntries() }()

	tmp, err := os.CreateTemp(filepath.Dir(d.Path), ".kdbx-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if err := gokeepasslib.NewEncoder(tmp).Encode(d.db); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("keepass: unable to encode database: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, d.Path)
}

// promptPassword obtains the database password, from PromptPassword if set,
// otherwise from the controlling terminal without echo.
func (d *KeepassDriver) promptPassword() (string, error) {
	if d.PromptPassword != nil {
		return d.PromptPassword()
	}
	fmt.Fprintf(os.Stderr, "Password for %s: ", d.Path)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("keepass: unable to read password: %w", err)
	}
	return string(pw), nil
}

// buildEntry converts a Secret into a KeePass entry with a protected password.
func buildEntry(secret Secret) gokeepasslib.Entry {
	entry := gokeepasslib.NewEntry()
	entry.Values = append(entry.Values,
		gokeepasslib.ValueData{Key: "Title", Value: gokeepasslib.V{Content: secret.Key}},
		gokeepasslib.ValueData{Key: "Password", Value: gokeepasslib.V{
			Content:   secret.Value,
			Protected: wrappers.NewBoolWrapper(true),
		}},
	)
	if secret.CreatedAt != nil {
		entry.Times.CreationTime = &wrappers.TimeWrapper{Formatted: true, Time: *secret.CreatedAt}
	}
	if secret.UpdatedAt != nil {
		entry.Times.LastModificationTime = &wrappers.TimeWrapper{Formatted: true, Time: *secret.UpdatedAt}
	}
	return entry
}

// entryToSecret converts a KeePass entry back into a Secret.
func entryToSecret(e *gokeepasslib.Entry) *Secret {
	s := &Secret{
		Key:   e.GetTitle(),
		Value: e.GetPassword(),
	}
	if e.Times.CreationTime != nil {
		t := e.Times.CreationTime.Time
		s.CreatedAt = &t
	}
	if e.Times.LastModificationTime != nil {
		t := e.Times.LastModificationTime.Time
		s.UpdatedAt = &t
	}
	return s
}

// locateEntry searches the group tree for an entry whose title matches key and
// returns the containing group and the entry's index within it, or (nil, -1).
func locateEntry(groups []gokeepasslib.Group, key string) (*gokeepasslib.Group, int) {
	for i := range groups {
		g := &groups[i]
		for j := range g.Entries {
			if g.Entries[j].GetTitle() == key {
				return g, j
			}
		}
		if fg, idx := locateEntry(g.Groups, key); fg != nil {
			return fg, idx
		}
	}
	return nil, -1
}
