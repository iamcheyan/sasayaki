package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Paths struct {
	ConfigHome string
	DataHome   string
	StateHome  string
	Runtime    string
}

type Config struct {
	Language string `json:"language"`
}

func NewPaths() Paths {
	home, _ := os.UserHomeDir()
	configHome := envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dataHome := envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	stateHome := envOr("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		runtime = filepath.Join("/tmp", "sasayaki-"+os.Getenv("USER"))
	}
	return Paths{
		ConfigHome: filepath.Join(configHome, "sasayaki"),
		DataHome:   filepath.Join(dataHome, "sasayaki"),
		StateHome:  filepath.Join(stateHome, "sasayaki"),
		Runtime:    filepath.Join(runtime, "sasayaki"),
	}
}

func (p Paths) ConfigFile() string   { return filepath.Join(p.ConfigHome, "config.json") }
func (p Paths) ModelDir() string     { return filepath.Join(p.DataHome, "models", "sensevoice") }
func (p Paths) VenvDir() string      { return filepath.Join(p.DataHome, "runtime", "venv") }
func (p Paths) EngineScript() string { return filepath.Join(p.DataHome, "runtime", "engine.py") }
func (p Paths) ServiceFile() string {
	return filepath.Join(filepath.Dir(p.ConfigHome), "systemd", "user", "sasayaki.service")
}
func (p Paths) Socket() string        { return filepath.Join(p.Runtime, "sasayaki.sock") }
func (p Paths) EngineSocket() string  { return filepath.Join(p.Runtime, "engine.sock") }
func (p Paths) RecordingsDir() string { return filepath.Join(p.StateHome, "recordings") }

func (p Paths) Ensure() error {
	for _, dir := range []string{p.ConfigHome, p.DataHome, p.StateHome, p.Runtime, p.RecordingsDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func Load(p Paths) (Config, error) {
	c := Config{Language: "auto"}
	b, err := os.ReadFile(p.ConfigFile())
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func Save(p Paths, c Config) error {
	if err := os.MkdirAll(p.ConfigHome, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.ConfigFile(), append(b, '\n'), 0o600)
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
