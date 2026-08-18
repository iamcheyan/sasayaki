package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewPathsHonorsXDGOverrides(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "cfg"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(base, "run"))
	t.Setenv("HOME", filepath.Join(base, "home"))

	p := NewPaths()
	for _, dir := range []string{p.ConfigHome, p.DataHome, p.StateHome, p.Runtime} {
		if !strings.HasPrefix(dir, base) {
			t.Errorf("path %q escapes XDG override root %q", dir, base)
		}
	}
	for _, file := range []string{p.ConfigFile(), p.ServiceFile(), p.Socket(), p.ModelDir(), p.RecordingsDir()} {
		if !strings.HasPrefix(file, base) {
			t.Errorf("path %q escapes XDG override root %q", file, base)
		}
	}
	if p.Socket() != filepath.Join(base, "run", "sasayaki", "sasayaki.sock") {
		t.Errorf("socket = %q", p.Socket())
	}
}

func TestNewPathsFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(k, "")
	}
	p := NewPaths()
	if p.ConfigFile() != filepath.Join(home, ".config", "sasayaki", "config.json") {
		t.Errorf("config = %q", p.ConfigFile())
	}
	if !strings.HasPrefix(p.Runtime, os.TempDir()) {
		t.Errorf("runtime fallback = %q, want under %s", p.Runtime, os.TempDir())
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := testPaths(t)
	c := Default()
	c.Language = "ja"
	c.Retention = Duration(5 * time.Minute)
	c.KeepRecordings = true
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Language != "ja" || got.Retention != c.Retention || !got.KeepRecordings {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestSavePermissions(t *testing.T) {
	p := testPaths(t)
	if err := Save(p, Default()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %o, want 600", fi.Mode().Perm())
	}
	di, err := os.Stat(p.ConfigHome)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("config dir mode = %o, want 700", di.Mode().Perm())
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	p := testPaths(t)
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !configEqual(got, Default()) {
		t.Fatalf("defaults mismatch: got=%+v want=%+v", got, Default())
	}
}

func configEqual(a, b Config) bool {
	if a.SpeechModel != b.SpeechModel || a.Language != b.Language ||
		a.ShortcutMode != b.ShortcutMode || a.Retention != b.Retention ||
		a.KeepRecordings != b.KeepRecordings || a.VerboseTranscripts != b.VerboseTranscripts ||
		a.TranslationBinding != b.TranslationBinding {
		return false
	}
	if !sliceEq(a.VoiceBindings, b.VoiceBindings) {
		return false
	}
	return a.Translation == b.Translation
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLoadInvalidFileIsError(t *testing.T) {
	p := testPaths(t)
	if err := os.MkdirAll(p.ConfigHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ConfigFile(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for corrupt config")
	}
}

func TestLoadInvalidLanguageIsError(t *testing.T) {
	p := testPaths(t)
	if err := Save(p, Default()); err != nil {
		t.Fatal(err)
	}
	// Corrupt only the language value, bypassing Save validation.
	raw := []byte(`{"language":"klingon","retention":"10m"}`)
	if err := os.WriteFile(p.ConfigFile(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for unsupported language")
	}
}

func TestSaveRejectsInvalidConfig(t *testing.T) {
	p := testPaths(t)
	c := Default()
	c.Language = "nope"
	if err := Save(p, c); err == nil {
		t.Fatal("expected validation error")
	}
	if _, err := os.Stat(p.ConfigFile()); !os.IsNotExist(err) {
		t.Fatal("invalid config must not be written")
	}
}

func TestEnsureCreatesPrivateDirs(t *testing.T) {
	p := testPaths(t)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{p.ConfigHome, p.DataHome, p.StateHome, p.Runtime, p.RecordingsDir()} {
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %o, want 700", dir, fi.Mode().Perm())
		}
	}
}

func TestRetentionJSONIsHumanReadable(t *testing.T) {
	c := Default()
	c.Retention = Duration(90 * time.Second)
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"retention":"1m30s"`) {
		t.Errorf("retention JSON = %s", b)
	}
	got := Default()
	if err := json.Unmarshal([]byte(`{"retention":"2h"}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Retention != Duration(2*time.Hour) {
		t.Fatalf("parsed retention = %v", got.Retention)
	}
}

// testPaths returns isolated paths inside a temp dir so tests never touch a
// real home directory.
func testPaths(t *testing.T) Paths {
	t.Helper()
	base := t.TempDir()
	return Paths{
		ConfigHome: filepath.Join(base, "config"),
		DataHome:   filepath.Join(base, "data"),
		StateHome:  filepath.Join(base, "state"),
		Runtime:    filepath.Join(base, "runtime"),
	}
}
