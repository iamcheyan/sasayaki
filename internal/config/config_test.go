package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	base := t.TempDir()
	p := Paths{ConfigHome: filepath.Join(base, "config"), DataHome: filepath.Join(base, "data"), StateHome: filepath.Join(base, "state"), Runtime: filepath.Join(base, "runtime")}
	if err := Save(p, Config{Language: "ja"}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Language != "ja" {
		t.Fatalf("language = %q", got.Language)
	}
	if _, err := os.Stat(p.ConfigFile()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceUnitUsesXDGConfigRoot(t *testing.T) {
	p := Paths{ConfigHome: "/example/config/sasayaki"}
	want := "/example/config/systemd/user/sasayaki.service"
	if got := p.ServiceFile(); got != want {
		t.Fatalf("service path = %q, want %q", got, want)
	}
}
