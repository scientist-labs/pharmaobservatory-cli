// Package config resolves profiles and the config directory.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Built-in client ids. Public by design.
const (
	ProdClientID    = "client_01M56DAF2T2FGWVTQAXJF3A7QZ"
	StagingClientID = "client_01M5621VVCCE084WXQW2PR2M0R"
	ProdOrigin      = "https://pharmaobservatory.com"
	StagingOrigin   = "https://candidate.pharmaobservatory.com"
)

// Profile is one environment the CLI can talk to.
type Profile struct {
	Name     string `yaml:"-"`
	Origin   string `yaml:"origin,omitempty"`
	ClientID string `yaml:"client_id,omitempty"`
	APIBase  string `yaml:"api_base,omitempty"`
}

// Resource is the token audience for the profile.
func (p Profile) Resource() string { return p.Origin + "/api/v1" }

// Base is where requests go: api_base when set, else the origin.
func (p Profile) Base() string {
	if p.APIBase != "" {
		return p.APIBase
	}
	return p.Origin
}

// File is the on-disk config.yml.
type File struct {
	DefaultProfile string             `yaml:"default_profile,omitempty"`
	Profiles       map[string]Profile `yaml:"profiles,omitempty"`
}

var builtins = map[string]Profile{
	"prod":    {Origin: ProdOrigin, ClientID: ProdClientID},
	"staging": {Origin: StagingOrigin, ClientID: StagingClientID},
}

// Dir returns the config directory (not created).
func Dir() (string, error) {
	if d := os.Getenv("PO_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("AppData"); d != "" {
			return filepath.Join(d, "po"), nil
		}
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "po"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "po"), nil
}

// EnsureDir creates the config directory 0700 and returns it.
func EnsureDir() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

// Load reads config.yml; a missing file is an empty config.
func Load() (File, error) {
	var f File
	d, err := Dir()
	if err != nil {
		return f, err
	}
	b, err := os.ReadFile(filepath.Join(d, "config.yml"))
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("config.yml: %w", err)
	}
	return f, nil
}

// Save writes config.yml 0600 in the 0700 dir.
func Save(f File) error {
	d, err := EnsureDir()
	if err != nil {
		return err
	}
	b, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, "config.yml"), b, 0o600)
}

// SelectName applies the selection order: flag, $PO_PROFILE, default_profile, prod.
func SelectName(flag string, f File) string {
	switch {
	case flag != "":
		return flag
	case os.Getenv("PO_PROFILE") != "":
		return os.Getenv("PO_PROFILE")
	case f.DefaultProfile != "":
		return f.DefaultProfile
	}
	return "prod"
}

// Resolve merges the built-in profile (if any) with the file entry of the same name.
func Resolve(name string, f File) (Profile, error) {
	p, builtin := builtins[name]
	over, inFile := f.Profiles[name]
	if !builtin && !inFile {
		return Profile{}, fmt.Errorf("unknown profile %q (built-in: prod, staging; create one with po login --profile %s --origin URL)", name, name)
	}
	if over.Origin != "" {
		p.Origin = over.Origin
	}
	if over.ClientID != "" {
		p.ClientID = over.ClientID
	}
	if over.APIBase != "" {
		p.APIBase = over.APIBase
	}
	if p.ClientID == "" {
		p.ClientID = StagingClientID
	}
	if p.Origin == "" {
		return Profile{}, fmt.Errorf("profile %q has no origin", name)
	}
	p.Name = name
	return p, nil
}

// Upsert records a custom profile entry (only the non-empty fields) in the file.
func Upsert(f *File, name string, over Profile) {
	if f.Profiles == nil {
		f.Profiles = map[string]Profile{}
	}
	cur := f.Profiles[name]
	if over.Origin != "" {
		cur.Origin = over.Origin
	}
	if over.ClientID != "" {
		cur.ClientID = over.ClientID
	}
	if over.APIBase != "" {
		cur.APIBase = over.APIBase
	}
	f.Profiles[name] = cur
}
