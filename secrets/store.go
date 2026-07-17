package secrets

import (
	"errors"
	"time"
)

// Sentinel errors returned by Store. Driver implementations are expected to
// return ErrNotFound for missing keys so callers can use errors.Is.
var (
	// ErrNoDriver is returned when a Store is used without a Driver configured.
	ErrNoDriver = errors.New("secrets: no driver configured")
	// ErrEmptyKey is returned when an empty key is supplied.
	ErrEmptyKey = errors.New("secrets: key must not be empty")
	// ErrNotFound is returned when a secret does not exist.
	ErrNotFound = errors.New("secrets: not found")
	// ErrReadOnly is returned by a write operation on a read-only driver.
	ErrReadOnly = errors.New("secrets: driver is read-only")
)

type Secret struct {
	Key       string
	Value     string
	CreatedAt *time.Time
	UpdatedAt *time.Time
}

type Driver interface {
	// Unlock prepares the backend before any other method is used.
	// Implementations must be safe to call more than once.
	Unlock() error
	// Get returns the secret stored under the given key, or ErrNotFound.
	Get(string) (*Secret, error)
	// Set creates or replaces a secret.
	Set(Secret) error
	// List returns every stored secret.
	List() ([]Secret, error)
	// Delete removes the secret stored under the given key.
	Delete(string) error
}

// Store is the high-level API over a Driver. Use NewStore to build one.
type Store struct {
	Driver Driver

	unlocked bool
}

// NewStore returns a Store backed by the given driver.
func NewStore(driver Driver) *Store {
	return &Store{Driver: driver}
}

// Unlock unlocks the underlying driver. It is called automatically by the other
// methods, but can be invoked explicitly to surface unlock errors early. It is
// idempotent: the driver is only unlocked once.
func (s *Store) Unlock() error {
	if s.Driver == nil {
		return ErrNoDriver
	}
	if s.unlocked {
		return nil
	}
	if err := s.Driver.Unlock(); err != nil {
		return err
	}
	s.unlocked = true
	return nil
}

// Get returns the secret stored under key.
func (s *Store) Get(key string) (*Secret, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}
	if err := s.Unlock(); err != nil {
		return nil, err
	}
	return s.Driver.Get(key)
}

// GetValue returns just the value of the secret stored under key.
func (s *Store) GetValue(key string) (*string, error) {
	secret, err := s.Get(key)
	if err != nil {
		return nil, err
	}
	return &secret.Value, nil
}

// Has reports whether a secret exists for key.
func (s *Store) Has(key string) (bool, error) {
	if _, err := s.Get(key); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Set creates or replaces a secret, stamping its timestamps. CreatedAt is
// preserved when the secret already exists; UpdatedAt is always refreshed.
func (s *Store) Set(secret Secret) error {
	if secret.Key == "" {
		return ErrEmptyKey
	}
	if err := s.Unlock(); err != nil {
		return err
	}

	now := time.Now()
	secret.UpdatedAt = &now
	if secret.CreatedAt == nil {
		if existing, err := s.Driver.Get(secret.Key); err == nil && existing != nil && existing.CreatedAt != nil {
			secret.CreatedAt = existing.CreatedAt
		} else {
			secret.CreatedAt = &now
		}
	}
	return s.Driver.Set(secret)
}

// SetValue stores value under key, creating the secret if it does not exist.
func (s *Store) SetValue(key string, value string) error {
	return s.Set(Secret{Key: key, Value: value})
}

// List returns every stored secret.
func (s *Store) List() ([]Secret, error) {
	if err := s.Unlock(); err != nil {
		return nil, err
	}
	return s.Driver.List()
}

// Delete removes the secret stored under key.
func (s *Store) Delete(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	if err := s.Unlock(); err != nil {
		return err
	}
	return s.Driver.Delete(key)
}
