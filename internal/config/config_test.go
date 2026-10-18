package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirOrder(t *testing.T) {
	t.Setenv("PO_CONFIG_DIR", "/x/po")
	if d, _ := Dir(); d != "/x/po" {
		t.Fatal(d)
	}
	t.Setenv("PO_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if d, _ := Dir(); d != "/xdg/po" {
		t.Fatal(d)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/u")
	if d, _ := Dir(); d != "/home/u/.config/po" && !strings.HasSuffix(d, filepath.Join(".config", "po")) {
		t.Fatal(d)
	}
}

func TestBuiltinsAndOverrides(t *testing.T) {
	p, err := Resolve("prod", File{})
	if err != nil || p.Origin != ProdOrigin || p.ClientID != ProdClientID || p.Resource() != ProdOrigin+"/api/v1" || p.Base() != ProdOrigin {
		t.Fatalf("%+v %v", p, err)
	}
	s, _ := Resolve("staging", File{})
	if s.ClientID != StagingClientID || s.Origin != StagingOrigin {
		t.Fatalf("%+v", s)
	}
	f := File{Profiles: map[string]Profile{"staging": {APIBase: "http://localhost:3000"}}}
	o, _ := Resolve("staging", f)
	if o.Base() != "http://localhost:3000" || o.Origin != StagingOrigin || o.Resource() != StagingOrigin+"/api/v1" {
		t.Fatalf("%+v", o)
	}
}

func TestCustomProfileDefaultsToStagingClient(t *testing.T) {
	var f File
	Upsert(&f, "local", Profile{Origin: "https://x.example"})
	p, err := Resolve("local", f)
	if err != nil || p.ClientID != StagingClientID || p.Origin != "https://x.example" {
		t.Fatalf("%+v %v", p, err)
	}
	Upsert(&f, "local", Profile{ClientID: "c2", APIBase: "http://a"})
	p, _ = Resolve("local", f)
	if p.ClientID != "c2" || p.APIBase != "http://a" || p.Origin != "https://x.example" {
		t.Fatalf("%+v", p)
	}
	if _, err := Resolve("nope", f); err == nil {
		t.Fatal("unknown profile resolved")
	}
	if _, err := Resolve("empty", File{Profiles: map[string]Profile{"empty": {}}}); err == nil {
		t.Fatal("originless profile resolved")
	}
}

func TestSelectOrder(t *testing.T) {
	t.Setenv("PO_PROFILE", "")
	if SelectName("", File{}) != "prod" {
		t.Fatal("default")
	}
	if SelectName("", File{DefaultProfile: "staging"}) != "staging" {
		t.Fatal("file default")
	}
	t.Setenv("PO_PROFILE", "env")
	if SelectName("", File{DefaultProfile: "staging"}) != "env" {
		t.Fatal("env")
	}
	if SelectName("flag", File{}) != "flag" {
		t.Fatal("flag")
	}
}

func TestSaveLoad(t *testing.T) {
	t.Setenv("PO_CONFIG_DIR", filepath.Join(t.TempDir(), "po"))
	if f, err := Load(); err != nil || len(f.Profiles) != 0 {
		t.Fatal(f, err)
	}
	in := File{DefaultProfile: "local", Profiles: map[string]Profile{"local": {Origin: "https://x"}}}
	if err := Save(in); err != nil {
		t.Fatal(err)
	}
	out, err := Load()
	if err != nil || out.DefaultProfile != "local" || out.Profiles["local"].Origin != "https://x" {
		t.Fatalf("%+v %v", out, err)
	}
	d, _ := Dir()
	os.WriteFile(filepath.Join(d, "config.yml"), []byte("profiles: [broken"), 0o600)
	if _, err := Load(); err == nil {
		t.Fatal("broken yaml accepted")
	}
}
