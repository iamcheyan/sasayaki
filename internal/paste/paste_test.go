package paste

// Shared test scaffolding: the command runner fake both platform test files
// assert argv with. Platform behavior lives in paste_linux_test.go and
// paste_darwin_test.go.

// fakeRunner records every command and lets tests control LookPath results,
// per-call output (onRun) and failures (failRun/failStdin).
type fakeRunner struct {
	present   map[string]bool
	runs      []cmdCall
	stdin     []string
	failRun   map[string]bool
	failStdin map[string]bool
	onRun     func(name string, args []string) ([]byte, error)
}

type cmdCall struct {
	name string
	args []string
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if f.present[name] {
		return "/usr/bin/" + name, nil
	}
	return "", &lookPathError{name}
}

type lookPathError struct{ name string }

func (e *lookPathError) Error() string { return "not found: " + e.name }

func (f *fakeRunner) Run(name string, args ...string) ([]byte, error) {
	f.runs = append(f.runs, cmdCall{name, args})
	if f.failRun[name] {
		return []byte("paste failed"), &runError{name}
	}
	if f.onRun != nil {
		return f.onRun(name, args)
	}
	return nil, nil
}

func (f *fakeRunner) RunStdin(name string, args []string, stdin []byte) ([]byte, error) {
	f.runs = append(f.runs, cmdCall{name, args})
	f.stdin = append(f.stdin, string(stdin))
	if f.failStdin[name] {
		return []byte("copy failed"), &runError{name}
	}
	return nil, nil
}

type runError struct{ name string }

func (e *runError) Error() string { return e.name + " exited nonzero" }
