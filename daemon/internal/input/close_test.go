package input

import "testing"

func TestRequestCloseDoesNotWaitForInputThread(t *testing.T) {
	s := &Source{quit: make(chan struct{}), done: make(chan struct{})}
	s.RequestClose()
	s.RequestClose()
	select {
	case <-s.quit:
	default:
		t.Fatal("input exit was not requested")
	}
	select {
	case <-s.done:
		t.Fatal("request claimed that native teardown had already finished")
	default:
	}
}
