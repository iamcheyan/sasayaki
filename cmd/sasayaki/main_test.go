package main

import "testing"

func TestHasJSONFlag(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"--json"}, true},
		{[]string{"-json"}, true},
		// The macOS menubar invokes `deliver <wav> --no-paste --json`: the
		// positional WAV path comes BEFORE --json. Go's flag.Parse stops at
		// the first positional, so a naive flag.Parse-based check would miss
		// the trailing --json and silently emit plain text.
		{[]string{"/tmp/rec.wav", "--no-paste", "--json"}, true},
		{[]string{"/tmp/rec.wav", "--json", "--no-paste"}, true},
		{[]string{"--no-paste", "/tmp/rec.wav"}, false},
		{[]string{}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := hasJSONFlag(c.args); got != c.want {
			t.Errorf("hasJSONFlag(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}