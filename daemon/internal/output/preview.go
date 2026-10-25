package output

import "errors"

// SetLocalDryRun changes the destination of future output. The old destination
// is fully released first; virtual keys and queued taps are never replayed.
// A failed physical release keeps the old mode so its retries still reach
// SendInput. The caller keeps an independent press gate closed until its slot
// cancellation barriers finish.
func (a *Arbiter) SetLocalDryRun(on bool) error {
	if on == a.localDryRun {
		return nil
	}
	if err := a.ReleaseAll(); err != nil {
		return err
	}
	if len(a.keys) != 0 {
		return errors.New("output: local dry-run transition has pending releases")
	}
	a.localDryRun = on
	return nil
}

func (a *Arbiter) LocalDryRun() bool { return a.localDryRun }
