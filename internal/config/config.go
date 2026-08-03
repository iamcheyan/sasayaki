// Package config owns XDG path resolution, validated configuration and
// atomic persistence. Sasayaki never reads or writes paths owned by other
// applications.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Defaults for configurable behavior.
const (
	DefaultLanguage           = "auto"
	DefaultSpeechModel        = "sensevoice-int8"
	DefaultShortcutMode       = "toggle"
	DefaultRetention          = 10 * time.Minute
	MinRecording              = 300 * time.Millisecond
	DefaultVoiceBinding       = "ALT + A"
	DefaultTranslationBinding = "HANGUL"
)

// DefaultVoiceBindings are the Hyprland keybindings that trigger voice
// input toggle when none are configured.
var DefaultVoiceBindings = []string{DefaultVoiceBinding, "code:472"}

// SupportedLanguages are the SenseVoice model languages accepted in config.
var SupportedLanguages = []string{"auto", "zh", "ja", "en", "ko", "yue"}

// Duration marshals as a Go duration string ("10m") so the config file
// stays human-editable.
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Config is the validated user configuration. It is stored at
// $XDG_CONFIG_HOME/sasayaki/config.json and never shared.
type Config struct {
	// SpeechModel selects an installed local ASR model from Sasayaki's catalog.
	SpeechModel string `json:"speech_model"`
	// Language selects automatic or a fixed model language.
	Language string `json:"language"`
	// ShortcutMode is the desktop-shortcut contract. Only "toggle" is
	// implemented; portal push-to-talk is a future enhancement.
	ShortcutMode string `json:"shortcut_mode"`
	// Retention is how long temporary recordings are kept before cleanup.
	Retention Duration `json:"retention"`
	// KeepRecordings retains recordings indefinitely for diagnostics.
	KeepRecordings bool `json:"keep_recordings"`
	// VerboseTranscripts opt-in logging of full transcribed text.
	VerboseTranscripts bool `json:"verbose_transcripts"`
	// Translation is optional. It targets any OpenAI-compatible chat endpoint
	// and belongs solely to Sasayaki; it is never read from OpenCode/Sumika.
	Translation TranslationConfig `json:"translation"`
	// VoiceBindings are Hyprland keybindings that trigger voice toggle.
	// Consumed by `sasayaki bindings` for desktop integration; ignored when
	// running standalone. Empty means use the defaults below.
	VoiceBindings []string `json:"voice_bindings,omitempty"`
	// TranslationBinding is the Hyprland keybinding that triggers translated
	// voice input. Empty means use the default.
	TranslationBinding string `json:"translation_binding,omitempty"`
}

type TranslationConfig struct {
	Enabled        bool   `json:"enabled"`
	BaseURL        string `json:"base_url,omitempty"`
	Model          string `json:"model,omitempty"`
	APIKey         string `json:"api_key,omitempty"`
	TargetLanguage string `json:"target_language,omitempty"`
}

// Default returns a configuration with documented defaults.
func Default() Config {
	return Config{
		SpeechModel:        DefaultSpeechModel,
		Language:           DefaultLanguage,
		ShortcutMode:       DefaultShortcutMode,
		Retention:          Duration(DefaultRetention),
		VoiceBindings:      DefaultVoiceBindings,
		TranslationBinding: DefaultTranslationBinding,
	}
}

// Validate returns an error describing the first invalid field.
func (c Config) Validate() error {
	if !contains(SupportedLanguages, c.Language) {
		return fmt.Errorf("language %q is not supported (use one of %v)", c.Language, SupportedLanguages)
	}
	if c.SpeechModel == "" {
		return errors.New("speech_model must not be empty")
	}
	if c.ShortcutMode != "toggle" {
		return fmt.Errorf("shortcut_mode %q is not supported (only \"toggle\")", c.ShortcutMode)
	}
	if c.Retention < 0 {
		return errors.New("retention must not be negative")
	}
	if c.Translation.Enabled {
		if c.Translation.BaseURL == "" || c.Translation.Model == "" || c.Translation.TargetLanguage == "" {
			return errors.New("translation requires base_url, model and target_language when enabled")
		}
	}
	return nil
}

// Paths resolves every location Sasayaki owns from the XDG environment.
// Constructing Paths is cheap; NewPaths reads the environment once.
type Paths struct {
	ConfigHome string
	DataHome   string
	StateHome  string
	Runtime    string
}

// NewPaths resolves XDG base directories. When an XDG variable is unset the
// XDG base-directory specification defaults are used; when XDG_RUNTIME_DIR is
// unset a per-user directory under /tmp is used so the control socket still
// works on unusual setups.
func NewPaths() Paths {
	home, _ := os.UserHomeDir()
	configHome := envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dataHome := envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	stateHome := envOr("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		runtime = filepath.Join(os.TempDir(), "sasayaki-"+os.Getenv("USER"))
	}
	return Paths{
		ConfigHome: filepath.Join(configHome, "sasayaki"),
		DataHome:   filepath.Join(dataHome, "sasayaki"),
		StateHome:  filepath.Join(stateHome, "sasayaki"),
		Runtime:    filepath.Join(runtime, "sasayaki"),
	}
}

func (p Paths) ConfigFile() string { return filepath.Join(p.ConfigHome, "config.json") }

// ModelDirFor is the private directory for one speech model. Keeping each
// backend separate prevents identically named ONNX files from colliding.
func (p Paths) ModelDirFor(id string) string { return filepath.Join(p.DataHome, "models", id) }

// ModelDir is retained for the original SenseVoice installation path so an
// existing installation continues to work after upgrading.
func (p Paths) ModelDir() string   { return filepath.Join(p.DataHome, "models", "sensevoice") }
func (p Paths) RuntimeDir() string { return filepath.Join(p.DataHome, "runtime") }
func (p Paths) VenvDir() string    { return filepath.Join(p.RuntimeDir(), "venv") }
func (p Paths) EngineScript() string {
	return filepath.Join(p.RuntimeDir(), "engine.py")
}
func (p Paths) RequirementsFile() string {
	return filepath.Join(p.RuntimeDir(), "requirements.txt")
}
func (p Paths) VenvMarker() string {
	return filepath.Join(p.VenvDir(), "sasayaki.installed")
}
func (p Paths) ServiceFile() string {
	return filepath.Join(filepath.Dir(p.ConfigHome), "systemd", "user", "sasayaki.service")
}
func (p Paths) Socket() string        { return filepath.Join(p.Runtime, "sasayaki.sock") }
func (p Paths) RecordingsDir() string { return filepath.Join(p.StateHome, "recordings") }

// Ensure creates every private directory Sasayaki owns with 0700
// permissions. It is idempotent.
func (p Paths) Ensure() error {
	for _, dir := range []string{p.ConfigHome, p.DataHome, p.StateHome, p.Runtime, p.RecordingsDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// Load reads and validates config.json. A missing file yields defaults;
// a present but invalid file is an error so the user is never silently
// served a guessed configuration.
func Load(p Paths) (Config, error) {
	c := Default()
	b, err := os.ReadFile(p.ConfigFile())
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("config %s: %w", p.ConfigFile(), err)
	}
	if err := c.Validate(); err != nil {
		return c, fmt.Errorf("config %s: %w", p.ConfigFile(), err)
	}
	return c, nil
}

// Save writes config atomically: the payload goes to a temporary file in the
// same directory, is fsynced, and is renamed over the target. A crash can
// never truncate a previously good config.
func Save(p Paths, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(p.ConfigHome, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	temp, err := os.CreateTemp(p.ConfigHome, "config.json.tmp*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(b); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, p.ConfigFile())
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
