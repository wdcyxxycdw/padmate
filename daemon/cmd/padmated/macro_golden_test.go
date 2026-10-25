package main

import "testing"

// TestGoldenMacros replays testdata/macro-replay/trace.jsonl with the
// profile there, which only runs macros (SCR-50), as text and with
// --decisions, and compares the transcript with replay.golden: the keys
// of each run, a press during a run ignored, the keys of a run released
// when it ends, and a missing macro reported once.
func TestGoldenMacros(t *testing.T) {
	const dir = "testdata/macro-replay/"
	got := transcript(t, "--profile", dir+"profile.json", "--fake-input", dir+"trace.jsonl", "--dry-run")
	checkGolden(t, dir+"replay.golden", got)
}
