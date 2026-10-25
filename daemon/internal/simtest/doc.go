// Package simtest is the shared test harness of the output, engine and
// script tests (ENG-19). Its trace notation writes input edges and output
// key events as one line of text, such as "+LCtrl@0 +Z@0 -Z@20 -LCtrl@20",
// so tests state expected behavior in the same form they report it.
//
// A Sim puts a real output.Arbiter on a clock.Fake, writing to a Recorder,
// a fake output.Sender that records what it is offered, tracks which keys
// are physically down and fails on demand. Every Sim checks with
// AssertNoneHeld that its test leaves no key held (CLAUDE.md invariant 1).
//
// A SyncRecorder is a Recorder behind a lock, for tests whose Sender runs
// on another goroutine, such as an output.Loop's.
//
// ReadGolden, RewriteGolden and LineDiff serve the golden tests of the
// slot and the supervisor, whose trace files FormatTraceLines writes.
//
// The package is cross-platform and drives time only through clock.Fake,
// except SyncRecorder.WaitFor, which waits in real time, at most
// WaitLimit, for another goroutine to send.
package simtest
