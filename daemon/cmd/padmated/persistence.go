package main

import (
	"fmt"
	"time"
)

// Persistence never runs on an input, slot or output thread. Shutdown calls
// stopPersistence after key release, so a slow file system cannot hold keys.
func (r *liveRun) startPersistence() {
	opts := r.cfg.opts
	if !opts.librarySelection() || opts.memory == nil {
		return
	}
	opts.memory.SetLastProfile(opts.profileID)
	r.persistStop, r.persistDone = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(r.persistDone)
		defer func() {
			if v := recover(); v != nil {
				r.persistErr = fmt.Errorf("state persistence panic: %v", v)
			}
		}()
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		failing := false
		flush := func(stopping bool) {
			err := opts.memory.Flush(opts.library)
			if stopping {
				r.persistErr = err
			}
			if err != nil && !failing && !stopping {
				select {
				case <-r.persistStop:
					// A slow flush may finish after shutdown closed the log.
				default:
					r.log.Warn("state not saved", "err", err)
				}
			}
			failing = err != nil
		}
		for {
			select {
			case <-r.persistStop:
				flush(true)
				return
			case <-tick.C:
				flush(false)
			}
		}
	}()
}

func (r *liveRun) stopPersistence() {
	if r.persistStop == nil {
		return
	}
	close(r.persistStop)
	select {
	case <-r.persistDone:
		if r.persistErr != nil {
			r.log.Warn("final state not saved", "err", r.persistErr)
		}
	case <-time.After(time.Second):
		r.log.Warn("state persistence did not stop within 1s")
	}
}
