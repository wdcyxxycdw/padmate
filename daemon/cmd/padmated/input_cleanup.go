package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// This budget starts only after Run and the complete output release have ended.
const inputCleanupWait = time.Second

type inputCloseRequester interface {
	// RequestClose asks T-input to quit without waiting or making SDL calls.
	RequestClose()
}

// cleanupCall contains both panic and Goexit at a cleanup worker boundary.
// Its buffered reply remains safe after a timeout abandoned the waiting owner.
func cleanupCall(reply chan<- error, f func() error) {
	finished := false
	var err error
	defer func() {
		if !finished {
			fault := any("runtime.Goexit")
			if p := recover(); p != nil {
				fault = p
			}
			err = errors.Join(err, fmt.Errorf("input cleanup worker stopped: %v", fault))
		}
		reply <- err
	}()
	err = f()
	finished = true
}

// stopInput bounds only the physical feedback/input tail. The worker preserves
// feedback-before-Close in the ordinary path. Once abandoned, it cannot touch
// keyboard output: supervisor Run and T-output have already completely ended.
// reported is an input failure already reflected in the exit code and logs.
func (r *liveRun) stopInput(reported error) error {
	reply := make(chan error, 1)
	go cleanupCall(reply, func() error { return r.awaitInputCleanup(reported) })
	return <-reply
}

func (r *liveRun) awaitInputCleanup(reported error) error {
	var closeOnce sync.Once
	closeSource := func() {
		if r.src != nil {
			closeOnce.Do(r.src.Close)
		}
	}
	requestExit := func() {
		if src, ok := r.src.(inputCloseRequester); ok {
			src.RequestClose()
		} else if r.src != nil {
			// Compatibility for injected sources without the prompt request API.
			// Close still runs at a protected worker boundary and never delays us.
			go cleanupCall(make(chan error, 1), func() error { closeSource(); return nil })
		}
	}
	result := make(chan error, 1)
	go cleanupCall(result, func() error {
		r.effects.close()
		var err error
		if r.effects != nil {
			err = r.effects.failure // done closed before close returned
		}
		closeSource()
		if r.src != nil {
			if sourceErr := r.src.Err(); !errors.Is(sourceErr, reported) {
				err = errors.Join(err, sourceErr)
			}
		}
		return err
	})
	timer := time.NewTimer(inputCleanupWait)
	defer timer.Stop()
	select {
	case err := <-result:
		if err != nil {
			requestExit()
		}
		return err
	case <-timer.C:
		// Feedback may be waiting for a stuck T-input command. Request quit
		// now so that a recovered driver need not wait for feedback to end.
		requestExit()
		return fmt.Errorf("input cleanup did not stop within %v: %w", inputCleanupWait, context.DeadlineExceeded)
	}
}
