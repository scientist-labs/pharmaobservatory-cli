package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// FakeKeyring is an in-memory keychain; the real one is never touched in tests.
type FakeKeyring struct {
	M   map[string]string
	Err error
}

func NewFake() *FakeKeyring { return &FakeKeyring{M: map[string]string{}} }

func (f *FakeKeyring) Get(s, u string) (string, error) {
	if f.Err != nil {
		return "", f.Err
	}
	v, ok := f.M[s+"/"+u]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}
func (f *FakeKeyring) Set(s, u, p string) error {
	if f.Err != nil {
		return f.Err
	}
	f.M[s+"/"+u] = p
	return nil
}
func (f *FakeKeyring) Delete(s, u string) error {
	if f.Err != nil {
		return f.Err
	}
	if _, ok := f.M[s+"/"+u]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.M, s+"/"+u)
	return nil
}

func cred() Credential {
	return Credential{RefreshToken: "rt", AccessToken: "at", ExpiresAt: time.Unix(1000, 0).UTC(), Sub: "u", Aud: "a", ClientID: "c", ObtainedAt: time.Unix(700, 0).UTC()}
}

func TestFileStoreRoundTripAndMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pharma")
	s := FileStore{Dir: dir}
	if _, err := s.Get("prod"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty store: %v", err)
	}
	if err := s.Set("prod", cred()); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("staging", Credential{Sub: "other"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("prod")
	if err != nil || got != cred() {
		t.Fatalf("%+v %v", got, err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(dir, "credentials.json"))
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("credentials.json mode %v", fi.Mode().Perm())
		}
		di, _ := os.Stat(dir)
		if di.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode %v", di.Mode().Perm())
		}
	}
	if err := s.Delete("prod"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("prod"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if o, _ := s.Get("staging"); o.Sub != "other" {
		t.Fatal("delete touched another profile")
	}
	if err := s.Delete("prod"); err != nil {
		t.Fatalf("delete of absent profile: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestFileStoreCorrupt(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "credentials.json"), []byte("{nope"), 0o600)
	s := FileStore{Dir: dir}
	if _, err := s.Get("p"); err == nil {
		t.Fatal("corrupt file accepted")
	}
	if err := s.Set("p", cred()); err == nil {
		t.Fatal("set over corrupt file")
	}
	if err := s.Delete("p"); err == nil {
		t.Fatal("delete over corrupt file")
	}
}

func TestKeychainStore(t *testing.T) {
	kr := NewFake()
	s := KeychainStore{KR: kr}
	if _, err := s.Get("prod"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.Set("prod", cred()); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get("prod"); err != nil || got != cred() {
		t.Fatalf("%+v %v", got, err)
	}
	if err := s.Delete("prod"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("prod"); err != nil {
		t.Fatalf("delete absent: %v", err)
	}
	kr.M["pharma/bad"] = "{nope"
	if _, err := s.Get("bad"); err == nil {
		t.Fatal("bad blob accepted")
	}
	kr.Err = errors.New("locked")
	if _, err := s.Get("x"); err == nil {
		t.Fatal("keyring error swallowed")
	}
	if s.Name() != "keychain" || (FileStore{}).Name() != "file" {
		t.Fatal("names")
	}
}

func TestOpenSelection(t *testing.T) {
	t.Setenv("PHARMA_CONFIG_DIR", t.TempDir())
	t.Setenv("PHARMA_CREDENTIAL_STORE", "")
	kr := NewFake()
	if s, _ := Open(kr, "prod"); s.Name() != "keychain" {
		t.Fatalf("working keychain: %s", s.Name())
	}
	if s, _ := Open(nil, "prod"); s.Name() != "file" {
		t.Fatal("nil keyring")
	}
	kr.Err = errors.New("no secret service")
	if s, _ := Open(kr, "prod"); s.Name() != "file" {
		t.Fatalf("broken keychain: %s", s.Name())
	}
	kr.Err = nil
	t.Setenv("PHARMA_CREDENTIAL_STORE", "file")
	if s, _ := Open(kr, "prod"); s.Name() != "file" {
		t.Fatal("PHARMA_CREDENTIAL_STORE=file ignored")
	}
}

func TestOpenPrefersFileWhenLoginFellBack(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PHARMA_CONFIG_DIR", dir)
	t.Setenv("PHARMA_CREDENTIAL_STORE", "")
	kr := NewFake()
	_ = FileStore{Dir: dir}.Set("prod", cred())
	if s, _ := Open(kr, "prod"); s.Name() != "file" {
		t.Fatal("fell-back login not found")
	}
	_ = KeychainStore{KR: kr}.Set("prod", cred())
	if s, _ := Open(kr, "prod"); s.Name() != "keychain" {
		t.Fatal("keychain entry must win")
	}
}
