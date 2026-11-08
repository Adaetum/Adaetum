// Package secrets keeps setup's resume credentials in the operating system's
// credential store (macOS Keychain, Linux Secret Service, Windows Credential
// Manager) and nowhere else. There is deliberately no file fallback: a machine
// with no credential store asks again each run instead of writing a plaintext
// cache to the repository or home directory.
package secrets

import (
	"errors"

	"github.com/Adaetum/Adaetum/installer/github"
	"github.com/zalando/go-keyring"
)

// Service names the installer's entries in the credential store.
const Service = "io.adaetum.installer"

// ErrNotFound means no value is stored under that name.
var ErrNotFound = errors.New("no saved credential")

// Store is a credential store. Credentials are grouped by namespace (one recovery
// repository) and named by key.
type Store interface {
	// Available reports whether this machine has a usable credential store.
	Available() bool
	Name() string
	Get(namespace, key string) (string, error)
	Set(namespace, key, value string) error
	Delete(namespace, key string) error
}

// Namespace groups one recovery repository's credentials, so moving to another
// repository never reuses the first one's. A checkout with no GitHub origin gets
// a shared local namespace.
func Namespace(originURL string) string {
	if n := github.Normalize(originURL); n != "" && n != "github.com/" {
		return n
	}
	return "local-checkout"
}

// Keyring is the operating system's credential store.
type Keyring struct{}

func account(namespace, key string) string { return namespace + ":" + key }

func (Keyring) Name() string { return "your operating system's credential store" }

func (Keyring) Available() bool {
	_, err := keyring.Get(Service, "availability-probe")
	return err == nil || errors.Is(err, keyring.ErrNotFound)
}

func (Keyring) Get(namespace, key string) (string, error) {
	v, err := keyring.Get(Service, account(namespace, key))
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

func (Keyring) Set(namespace, key, value string) error {
	if value == "" {
		return errors.New("refusing to save an empty credential")
	}
	return keyring.Set(Service, account(namespace, key), value)
}

func (Keyring) Delete(namespace, key string) error {
	err := keyring.Delete(Service, account(namespace, key))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Memory is an in-process store for tests and for runs where the user declined to
// save anything.
type Memory struct {
	data map[string]string
	Off  bool // report the store as unavailable
}

func (m *Memory) Name() string    { return "memory" }
func (m *Memory) Available() bool { return !m.Off }

func (m *Memory) Get(ns, key string) (string, error) {
	if v, ok := m.data[account(ns, key)]; ok {
		return v, nil
	}
	return "", ErrNotFound
}

func (m *Memory) Set(ns, key, value string) error {
	if m.data == nil {
		m.data = map[string]string{}
	}
	m.data[account(ns, key)] = value
	return nil
}

func (m *Memory) Delete(ns, key string) error {
	delete(m.data, account(ns, key))
	return nil
}
