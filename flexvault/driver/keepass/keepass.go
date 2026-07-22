// Package keepass implements a flexvault.VaultDriver backed by a KeePass
// (.kdbx) database file, registered under the name "keepass".
//
// Mapping to the flexconf secret model: a top-level group is a namespace, an
// entry within it is a key, and the entry's Password field is the secret
// value. Nesting beyond two levels is not addressable through flexconf.
package keepass

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tobischo/gokeepasslib/v3"
	w "github.com/tobischo/gokeepasslib/v3/wrappers"

	"github.com/sylvanld/go-flexconf/flexprompt"
	"github.com/sylvanld/go-flexconf/flexvault"
)

// CredPassword is the PromptRequest.ID this driver declares for the master
// password (see Credentials/InitCredentials and Unlock/Init).
const CredPassword = "password"

func init() {
	flexvault.Register("keepass", func() flexvault.VaultDriver { return New() })
}

type keepassConfig struct {
	Path     string `flexconf:"path"`
	KeyFile  string `flexconf:"keyfile"` // path to key file (non-secret); contents read at Unlock
	ReadOnly bool   `flexconf:"readonly"`
}

type driver struct {
	cfg        keepassConfig
	configured bool
	db         *gokeepasslib.Database // decrypted database; nil when locked
}

// New returns an unconfigured KeePass driver. Settings arrive via Configure;
// the .kdbx file is opened only at Unlock.
func New() flexvault.VaultDriver { return &driver{} }

func (d *driver) Name() string { return "keepass" }

func (d *driver) Configure(decode func(any) error) error {
	var c keepassConfig
	if err := decode(&c); err != nil {
		return err
	}
	if c.Path == "" {
		return errors.New("keepass: path is required")
	}
	d.cfg = c
	d.configured = true
	return nil
}

func (d *driver) Capabilities() flexvault.Capabilities {
	if !d.configured {
		return flexvault.Capabilities{}
	}
	return flexvault.Capabilities{Writable: !d.cfg.ReadOnly, Creatable: true}
}

func (d *driver) Credentials() []flexprompt.PromptRequest {
	return []flexprompt.PromptRequest{{
		ID:       CredPassword,
		Label:    "KeePass master password (" + d.cfg.Path + ")",
		Secret:   true,
		Optional: d.cfg.KeyFile != "", // a keyfile may suffice on its own
	}}
}

// credentials builds the composite DBCredentials from the answers and the
// configured keyfile.
func (d *driver) credentials(answers map[string]string) (*gokeepasslib.DBCredentials, error) {
	pw := answers[CredPassword]
	switch {
	case d.cfg.KeyFile != "" && pw != "":
		return gokeepasslib.NewPasswordAndKeyCredentials(pw, d.cfg.KeyFile)
	case d.cfg.KeyFile != "":
		return gokeepasslib.NewKeyCredentials(d.cfg.KeyFile)
	case pw != "":
		return gokeepasslib.NewPasswordCredentials(pw), nil
	default:
		return nil, fmt.Errorf("keepass: no password or keyfile provided: %w", flexvault.ErrAuth)
	}
}

func (d *driver) Unlock(_ context.Context, answers map[string]string) error {
	if !d.configured {
		return flexvault.ErrNotConfigured
	}
	f, err := os.Open(d.cfg.Path)
	if err != nil {
		return fmt.Errorf("keepass: opening %s: %w", d.cfg.Path, err)
	}
	defer f.Close()

	creds, err := d.credentials(answers)
	if err != nil {
		return err
	}
	db := gokeepasslib.NewDatabase()
	db.Credentials = creds
	if err := gokeepasslib.NewDecoder(f).Decode(db); err != nil {
		if errors.Is(err, gokeepasslib.ErrInvalidDatabaseOrCredentials) ||
			strings.Contains(err.Error(), "Wrong password") {
			return fmt.Errorf("keepass: %v: %w", err, flexvault.ErrAuth)
		}
		return fmt.Errorf("keepass: decoding %s: %w", d.cfg.Path, err)
	}
	if err := db.UnlockProtectedEntries(); err != nil {
		return fmt.Errorf("keepass: unlocking protected entries: %w", err)
	}
	d.db = db
	return nil
}

func (d *driver) Lock() error {
	// Drop the decrypted database (and, with it, the retained credentials).
	d.db = nil
	return nil
}

// root returns the database's root group, under which namespace groups live.
func (d *driver) root() (*gokeepasslib.Group, error) {
	if d.db == nil {
		return nil, flexvault.ErrLocked
	}
	if len(d.db.Content.Root.Groups) == 0 {
		return nil, errors.New("keepass: database has no root group")
	}
	return &d.db.Content.Root.Groups[0], nil
}

func (d *driver) findGroup(root *gokeepasslib.Group, name string) *gokeepasslib.Group {
	for i := range root.Groups {
		if root.Groups[i].Name == name {
			return &root.Groups[i]
		}
	}
	return nil
}

// findEntry returns the first entry in g titled key (documented convention:
// titles unique within a group; first match wins on a collision).
func (d *driver) findEntry(g *gokeepasslib.Group, key string) *gokeepasslib.Entry {
	for i := range g.Entries {
		if g.Entries[i].GetTitle() == key {
			return &g.Entries[i]
		}
	}
	return nil
}

func (d *driver) Get(_ context.Context, addr string) (string, error) {
	root, err := d.root()
	if err != nil {
		return "", err
	}
	ns, key, err := flexvault.ParseAddress(addr)
	if err != nil {
		return "", err
	}
	g := d.findGroup(root, ns)
	if g == nil {
		return "", fmt.Errorf("keepass: namespace %q: %w", ns, flexvault.ErrNotFound)
	}
	e := d.findEntry(g, key)
	if e == nil {
		return "", fmt.Errorf("keepass: %s: %w", addr, flexvault.ErrNotFound)
	}
	return e.GetPassword(), nil
}

func (d *driver) Set(_ context.Context, addr string, value string) error {
	root, err := d.root()
	if err != nil {
		return err
	}
	if d.cfg.ReadOnly {
		return fmt.Errorf("keepass: %s is read-only: %w", d.cfg.Path, flexvault.ErrReadOnly)
	}
	ns, key, err := flexvault.ParseAddress(addr)
	if err != nil {
		return err
	}
	g := d.findGroup(root, ns)
	if g == nil {
		// Auto-create the namespace group.
		ng := gokeepasslib.NewGroup()
		ng.Name = ns
		root.Groups = append(root.Groups, ng)
		g = &root.Groups[len(root.Groups)-1]
	}
	if e := d.findEntry(g, key); e != nil {
		if i := e.GetPasswordIndex(); i >= 0 {
			e.Values[i].Value.Content = value
		} else {
			e.Values = append(e.Values, protectedValue("Password", value))
		}
	} else {
		ne := gokeepasslib.NewEntry()
		ne.Values = append(ne.Values,
			plainValue("Title", key),
			protectedValue("Password", value),
		)
		g.Entries = append(g.Entries, ne)
	}
	return d.save()
}

func (d *driver) List(_ context.Context, namespace string) ([]string, error) {
	root, err := d.root()
	if err != nil {
		return nil, err
	}
	if namespace == "" {
		names := make([]string, 0, len(root.Groups))
		for i := range root.Groups {
			names = append(names, root.Groups[i].Name)
		}
		return names, nil
	}
	g := d.findGroup(root, namespace)
	if g == nil {
		return []string{}, nil // unknown namespace: empty, not an error
	}
	keys := make([]string, 0, len(g.Entries))
	for i := range g.Entries {
		keys = append(keys, g.Entries[i].GetTitle())
	}
	return keys, nil
}

// save persists the in-memory database atomically (temp file + rename).
func (d *driver) save() error {
	if err := d.db.LockProtectedEntries(); err != nil {
		return fmt.Errorf("keepass: locking protected entries: %w", err)
	}
	defer d.db.UnlockProtectedEntries() //nolint:errcheck // restore in-memory view

	tmp, err := os.CreateTemp(filepath.Dir(d.cfg.Path), ".flexconf-*.kdbx")
	if err != nil {
		return fmt.Errorf("keepass: creating temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := gokeepasslib.NewEncoder(tmp).Encode(d.db); err != nil {
		tmp.Close()
		return fmt.Errorf("keepass: encoding %s: %w", d.cfg.Path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("keepass: closing temp file: %w", err)
	}
	if err := os.Rename(tmp.Name(), d.cfg.Path); err != nil {
		return fmt.Errorf("keepass: saving %s: %w", d.cfg.Path, err)
	}
	return nil
}

// InitCredentials declares the setup secrets for creating a new vault: a new
// master password entered twice.
func (d *driver) InitCredentials() []flexprompt.PromptRequest {
	return []flexprompt.PromptRequest{{
		ID:      CredPassword,
		Label:   "New KeePass master password (" + d.cfg.Path + ")",
		Secret:  true,
		Confirm: true,
		// Optional when a keyfile alone will secure the vault.
		Optional: d.cfg.KeyFile != "",
	}}
}

// Init creates a new, empty .kdbx at the configured path. It fails if the
// file already exists and does not leave the vault unlocked.
func (d *driver) Init(_ context.Context, answers map[string]string) error {
	if !d.configured {
		return flexvault.ErrNotConfigured
	}
	if _, err := os.Stat(d.cfg.Path); err == nil {
		return fmt.Errorf("keepass: %s already exists", d.cfg.Path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("keepass: checking %s: %w", d.cfg.Path, err)
	}
	creds, err := d.credentials(answers)
	if err != nil {
		return err
	}

	root := gokeepasslib.NewGroup()
	root.Name = "Root"
	db := gokeepasslib.NewDatabase(gokeepasslib.WithDatabaseKDBXVersion4())
	db.Credentials = creds
	db.Content.Root = &gokeepasslib.RootData{Groups: []gokeepasslib.Group{root}}

	if err := os.MkdirAll(filepath.Dir(d.cfg.Path), 0o700); err != nil {
		return fmt.Errorf("keepass: creating directory: %w", err)
	}
	f, err := os.OpenFile(d.cfg.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("keepass: creating %s: %w", d.cfg.Path, err)
	}
	defer f.Close()
	if err := db.LockProtectedEntries(); err != nil {
		return fmt.Errorf("keepass: locking protected entries: %w", err)
	}
	if err := gokeepasslib.NewEncoder(f).Encode(db); err != nil {
		return fmt.Errorf("keepass: encoding %s: %w", d.cfg.Path, err)
	}
	return nil
}

func plainValue(key, value string) gokeepasslib.ValueData {
	return gokeepasslib.ValueData{Key: key, Value: gokeepasslib.V{Content: value}}
}

func protectedValue(key, value string) gokeepasslib.ValueData {
	return gokeepasslib.ValueData{
		Key:   key,
		Value: gokeepasslib.V{Content: value, Protected: w.NewBoolWrapper(true)},
	}
}
