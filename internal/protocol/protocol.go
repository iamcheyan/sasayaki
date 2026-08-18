// Package protocol defines the versioned control protocol spoken over the
// Sasayaki user-service Unix socket. Messages are newline-delimited JSON.
package protocol

// Version is the wire protocol version. Bump it whenever messages change in
// a way a peer from another version could misread; clients send their own
// version and the service rejects mismatches.
const Version = 1

// Operations supported by the service.
const (
	OpStatus          = "status"
	OpToggle          = "toggle"
	OpTranslateToggle = "translate-toggle"
	OpTestToggle      = "test-toggle"
	OpTestSpeech      = "test-speech"
	OpTestTranslation = "test-translation"
	OpCancel          = "cancel"
	// OpDeliver transcribes an already-finalized recording produced outside
	// the service. On macOS the menubar app owns the microphone (TCC grants
	// follow the process that records), so it captures with AVAudioEngine
	// and hands the finished WAV to the service through this operation; the
	// service never records for a deliver.
	OpDeliver  = "deliver"
	OpDiagnose = "diagnose"
)

// Service states reported in State.Service.
const (
	ServiceRunning   = "running"
	ServiceStopped   = "stopped"
	ServiceUnhealthy = "unhealthy"
)

// Error codes distinguish user-action errors from internal service failures.
// The CLI treats user errors as predictable outcomes; service errors are
// reported as failures with detail.
const (
	ErrUnknownOperation = "unknown_operation"
	ErrBadVersion       = "bad_version"
	ErrInvalidRequest   = "invalid_request"

	// User-action errors.
	ErrStillTranscribing = "still_transcribing"
	ErrNotRecording      = "not_recording"
	ErrAlreadyRecording  = "already_recording"

	// Service/execution errors.
	ErrMicrophoneFailed    = "microphone_failed"
	ErrTooShort            = "too_short"
	ErrEmptySpeech         = "empty_speech"
	ErrModelFailed         = "model_failed"
	ErrTranslationFailed   = "translation_failed"
	ErrTranslationDisabled = "translation_disabled"
	ErrPasteFailed         = "paste_failed"
	ErrNotReady            = "not_ready"
	ErrInternal            = "internal"
)

// ErrorClass tells a client whether an error is a predictable user-action
// outcome or a service-side failure.
type ErrorClass string

const (
	ClassUser    ErrorClass = "user"
	ClassService ErrorClass = "service"
)

// Request is a client → service message. The payload fields serve the
// deliver operation; they are optional and additive, so a version-1 peer
// that does not know them simply ignores them (encoding/json drops
// unknown fields) — the wire version stays 1.
type Request struct {
	Version   int    `json:"version"`
	Operation string `json:"operation"`
	// Wav is an absolute path to a finalized 16 kHz mono s16 WAV for
	// OpDeliver. Empty for every other operation.
	Wav string `json:"wav,omitempty"`
	// Translate asks OpDeliver to run the translation step before pasting.
	Translate bool `json:"translate,omitempty"`
}

// Error is a typed service error carried in a Response.
type Error struct {
	Code  string     `json:"code"`
	Class ErrorClass `json:"class"`
	// Detail is a human-readable explanation; it may reference dependencies
	// or a recovery command but never contains full transcribed text.
	Detail string `json:"detail,omitempty"`
}

// NewError builds a typed service error.
func NewError(code string, class ErrorClass, detail string) *Error {
	return &Error{Code: code, Class: class, Detail: detail}
}

// Response is a service → client message. OK=false always carries a typed
// Error; transport failures (unreachable socket, undecodable message) are
// reported by the client without a Response at all.
type Response struct {
	Version int    `json:"version"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Error   *Error `json:"error,omitempty"`
	State   *State `json:"state,omitempty"`
	// Diagnostics is a diagnostics.Report for the diagnose operation.
	Diagnostics interface{} `json:"diagnostics,omitempty"`
}

// Phase is the current operation phase of the service.
type Phase string

const (
	PhaseIdle         Phase = "idle"
	PhaseRecording    Phase = "recording"
	PhaseTranscribing Phase = "transcribing"
	PhasePasting      Phase = "pasting"
	PhaseTranslating  Phase = "translating"
	PhaseSucceeded    Phase = "succeeded"
	PhaseFailed       Phase = "failed"
)

// Worker states reported in State.Worker.
const (
	WorkerWarm     = "warm"
	WorkerStarting = "starting"
	WorkerDead     = "dead"
)

// State is a full snapshot of service readiness and the last operation. The
// TUI, `sasayaki status` and `--json` integrations render from this struct
// only.
type State struct {
	Version int `json:"version"`
	// Service is running, stopped or unhealthy.
	Service string `json:"service"`
	// Phase is the current operation phase.
	Phase Phase `json:"phase"`

	// Readiness.
	Runtime      bool   `json:"runtime"`
	Model        bool   `json:"model"`
	SpeechModel  string `json:"speech_model,omitempty"`
	Language     string `json:"language,omitempty"`
	Translation  string `json:"translation,omitempty"`
	Microphone   bool   `json:"microphone"`
	Paste        bool   `json:"paste"`
	PasteBackend string `json:"paste_backend,omitempty"`
	// Worker is the model worker state: warm, starting or dead.
	Worker string `json:"worker,omitempty"`

	// MicLevel is a live 0-100 peak-ish level of the in-progress recording,
	// sampled from the capture while phase is recording. 0 when idle. Lets
	// the test overlay show real-time audio feedback like the reference UI.
	MicLevel int `json:"mic_level"`

	// Last operation result (complete final text: the translation when
	// translation is enabled, otherwise the transcript) and error, with the
	// phase it ended in.
	LastResult string `json:"last_result,omitempty"`
	// Transcript is the complete original speech text of the last operation,
	// before any translation. Empty until something was transcribed.
	Transcript string `json:"transcript,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	LastAt     string `json:"last_at,omitempty"`
	LastPhase  Phase  `json:"last_phase,omitempty"`
}
