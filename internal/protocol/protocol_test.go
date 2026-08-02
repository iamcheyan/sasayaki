package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequestRoundTrip(t *testing.T) {
	for _, op := range []string{OpStatus, OpToggle, OpCancel, OpDiagnose} {
		req := Request{Version: Version, Operation: op}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		var back Request
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatal(err)
		}
		if back != req {
			t.Fatalf("round trip %+v != %+v", back, req)
		}
	}
}

func TestResponseRoundTripWithDiagnostics(t *testing.T) {
	resp := Response{
		Version: Version,
		OK:      true,
		Message: "ok",
		State: &State{
			Phase:  PhaseRecording,
			Worker: WorkerWarm,
		},
		Diagnostics: map[string]any{"checks": []any{map[string]any{"name": "python3", "ok": true}}},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var back Response
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.State == nil || back.State.Phase != PhaseRecording || back.State.Worker != WorkerWarm {
		t.Fatalf("state lost in round trip: %+v", back.State)
	}
	diag, ok := back.Diagnostics.(map[string]any)
	if !ok {
		t.Fatalf("diagnostics should round-trip as a JSON object, got %T", back.Diagnostics)
	}
	if _, ok := diag["checks"]; !ok {
		t.Fatalf("diagnostics missing checks: %v", diag)
	}
}

func TestErrorConstruction(t *testing.T) {
	err := NewError(ErrNotRecording, ClassUser, "nothing to cancel")
	if err.Code != ErrNotRecording || err.Class != ClassUser {
		t.Fatalf("typed error = %+v", err)
	}
	data, _ := json.Marshal(err)
	if !strings.Contains(string(data), "not_recording") {
		t.Fatalf("error code should serialize by its wire name: %s", data)
	}
}

func TestErrorWireShape(t *testing.T) {
	err := NewError(ErrMicrophoneFailed, ClassService, "no mic")
	data, _ := json.Marshal(err)
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["code"] != "microphone_failed" || raw["class"] != "service" || raw["detail"] != "no mic" {
		t.Fatalf("error wire shape wrong: %s", data)
	}
}

func TestStateWireShape(t *testing.T) {
	st := State{
		Service:    ServiceRunning,
		Phase:      PhaseSucceeded,
		Runtime:    true,
		Model:      true,
		Worker:     WorkerWarm,
		LastResult: "translated text",
		Transcript: "original speech",
		LastPhase:  PhaseSucceeded,
	}
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"version", "service", "phase", "runtime", "model", "worker"} {
		if _, ok := raw[field]; !ok {
			t.Fatalf("state missing field %q in %s", field, data)
		}
	}
	// LastResult must be a string (never an object carrying text).
	if _, ok := raw["last_result"].(string); !ok {
		t.Fatalf("last_result must be a plain string: %s", data)
	}
	if got, ok := raw["transcript"].(string); !ok || got != "original speech" {
		t.Fatalf("transcript must be the complete original text as a string: %s", data)
	}
}

func TestPhaseNamesStable(t *testing.T) {
	// Phases are part of the wire contract; renaming them silently breaks
	// the TUI and the CLI.
	want := []string{"idle", "recording", "transcribing", "pasting", "succeeded", "failed"}
	got := []string{string(PhaseIdle), string(PhaseRecording), string(PhaseTranscribing), string(PhasePasting), string(PhaseSucceeded), string(PhaseFailed)}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("phase %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestUnknownOperationDecodes(t *testing.T) {
	data := []byte(`{"version":1,"operation":"delete-everything"}`)
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal should not fail on unknown op: %v", err)
	}
	if req.Operation != "delete-everything" {
		t.Fatalf("operation should decode verbatim, got %q", req.Operation)
	}
}
