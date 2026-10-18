// Package store holds the credential store: OS keychain with a 0600 file fallback.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/config"
)

// ErrNotFound means no credential is stored for the profile.
var ErrNotFound = errors.New("no stored credential")

// Credential is what is kept per profile. No id token, email, name, verifier or state.
type Credential struct {
	RefreshToken string    `json:"refresh_token"`
	AccessToken  string    `json:"access_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Sub          string    `json:"sub"`
	Aud          string    `json:"aud"`
	ClientID     string    `json:"client_id"`
	ObtainedAt   time.Time `json:"obtained_at"`
}

// Store persists credentials by profile name.
type Store interface {
	Get(profile string) (Credential, error)
	Set(profile string, c Credential) error
	Delete(profile string) error
	// Name says which backend is in use ("keychain" or "file").
	Name() string
}

// Keyring is the slice of go-keyring the store needs, so tests use a fake.
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
	Delete(service, user string) error
}

type realKeyring struct{}

func (realKeyring) Get(s, u string) (string, error) { return keyring.Get(s, u) }
func (realKeyring) Set(s, u, p string) error        { return keyring.Set(s, u, p) }
func (realKeyring) Delete(s, u string) error        { return keyring.Delete(s, u) }

// SystemKeyring is the real OS keychain.
func SystemKeyring() Keyring { return realKeyring{} }

const service = "po"

// KeychainStore stores one JSON blob per profile in the keychain.
type KeychainStore struct{ KR Keyring }

func (KeychainStore) Name() string { return "keychain" }

func (s KeychainStore) Get(profile string) (Credential, error) {
	v, err := s.KR.Get(service, profile)
	if errors.Is(err, keyring.ErrNotFound) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, err
	}
	var c Credential
	if err := json.Unmarshal([]byte(v), &c); err != nil {
		return Credential{}, fmt.Errorf("stored credential for %s is unreadable: %w", profile, err)
	}
	return c, nil
}

func (s KeychainStore) Set(profile string, c Credential) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.KR.Set(service, profile, string(b))
}

func (s KeychainStore) Delete(profile string) error {
	err := s.KR.Delete(service, profile)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// FileStore keeps credentials.json (0600) in the config directory, keyed by profile.
type FileStore struct{ Dir string }

func (FileStore) Name() string { return "file" }

func (s FileStore) path() string { return filepath.Join(s.Dir, "credentials.json") }

func (s FileStore) read() (map[string]Credential, error) {
	m := map[string]Credential{}
	b, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("credentials.json is unreadable: %w", err)
	}
	return m, nil
}

func (s FileStore) write(m map[string]Credential) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path())
}

func (s FileStore) Get(profile string) (Credential, error) {
	m, err := s.read()
	if err != nil {
		return Credential{}, err
	}
	c, ok := m[profile]
	if !ok {
		return Credential{}, ErrNotFound
	}
	return c, nil
}

func (s FileStore) Set(profile string, c Credential) error {
	m, err := s.read()
	if err != nil {
		return err
	}
	m[profile] = c
	return s.write(m)
}

func (s FileStore) Delete(profile string) error {
	m, err := s.read()
	if err != nil {
		return err
	}
	if _, ok := m[profile]; !ok {
		return nil
	}
	delete(m, profile)
	return s.write(m)
}

// Open picks the store for one profile. $PO_CREDENTIAL_STORE=file forces the file. Otherwise the
// keychain is used when a probe shows it works; if it does not (no Secret Service, locked, no
// D-Bus) the file is used. When the keychain works but holds nothing for this profile while the
// file does (a login that had to fall back), the file is used. The choice is made once per
// invocation and reported by Name, so the two stores are never mixed within one command.
func Open(kr Keyring, profile string) (Store, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	file := FileStore{Dir: dir}
	if os.Getenv("PO_CREDENTIAL_STORE") == "file" || kr == nil {
		return file, nil
	}
	kc := KeychainStore{KR: kr}
	_, err = kc.Get(profile)
	switch {
	case err == nil:
		return kc, nil
	case errors.Is(err, ErrNotFound):
		if _, ferr := file.Get(profile); ferr == nil {
			return file, nil
		}
		return kc, nil
	}
	return file, nil
}
