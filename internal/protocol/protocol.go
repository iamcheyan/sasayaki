// Package protocol defines the versioned control protocol spoken over the
// Sasayaki user-service Unix socket. Messages are newline-delimited JSON.
package protocol

// Version is the wire protocol version. Bump it whenever messages change in
// a way a peer from another version could misread; clients send their own
// version and the service rejects mismatches.
const Version = 1

// Operations supported by the service.
const (
	OpStatus   = "status"
	OpToggle   = "toggle"
	OpCancel   = "cancel"
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
	ErrMicrophoneFailed = "microphone_failed"
	ErrTooShort         = "too_short"
	ErrEmptySpeech      = "empty_speech"
	ErrModelFailed      = "model_failed"
	ErrPasteFailed      = "paste_failed"
	ErrNotReady         = "not_ready"
	ErrInternal         = "internal"
)

// ErrorClass tells a client whether an error is a predictable user-action
// outcome or a service-side failure.
type ErrorClass string

const (
	ClassUser    ErrorClass = "user"
	ClassService ErrorClass = "service"
)

// Request is a client → service message.
type Request struct {
	Version   int    `json:"version"`
	Operation string `json:"operation"`
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
	Microphone   bool   `json:"microphone"`
	Paste        bool   `json:"paste"`
	PasteBackend string `json:"paste_backend,omitempty"`
	// Worker is the model worker state: warm, starting or dead.
	Worker string `json:"worker,omitempty"`

	// Last operation result (transcript text, truncated for privacy in the
	// socket snapshot) and error, with the phase it ended in.
	LastResult string `json:"last_result,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	LastAt     string `json:"last_at,omitempty"`
	LastPhase  Phase  `json:"last_phase,omitempty"`
}
