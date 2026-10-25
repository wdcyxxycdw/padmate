package output

import (
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// ErrLoopClosed is returned by Do once the loop has ended.
var ErrLoopClosed = errors.New("output: the T-output loop is closed")

// stopRetries is how many times Stop resends releases that stay pending.
const stopRetries = 3

// LoopOptions configures a Loop.
type LoopOptions struct {
	// Guard runs on T-output before commands and timed work. It may change
	// the Arbiter's local dry-run destination and nonblocking Loop gates,
	// but must not wait for another owner or call Do.
	Guard   func(*Arbiter)
	Arbiter ArbiterOptions
	// Lock locks the loop goroutine to its OS thread for its whole life
	// (runtime.LockOSThread, never unlocked, so the thread exits with it).
	// padmated must set it: T-output is then one OS thread, the only caller
	// of the Sender (CLAUDE.md invariant 3).
	Lock bool
	// Errors receives, on the loop goroutine, errors of work the loop does
	// on its own: Advance at deadlines and before commands, the release of
	// BlockFor, the failures of Stop's release that a retry made good, and
	// the errors of the held-key log after the sweep. It must not block or
	// call the Loop, except Blocking. nil drops them.
	Errors func(error)
	// HeldFile is the path of the held-key log (OUT-14, ADR-0027). The loop
	// opens it for its whole run, on its goroutine, and first sweeps what
	// the last run left in it (Swept). Before every Send it adds the keys
	// the batch presses, and after it drops the keys the Sender left up, so
	// that the file has every key that may be down when the process is
	// killed. The keys the sweep could not release stay in it until a
	// closing BlockFor or Stop releases them. Stop and the release after a
	// panic remove it when no key is left in it. A failed write is
	// reported to Errors, and the keys still go out. Empty keeps no log and
	// sweeps nothing: the dry run and most tests.
	HeldFile string
	// KeyDown reports whether the system has the key of e down: the sweep
	// releases only the keys of the log it reports down, and the menu mask
	// key when it reports that down. nil means AsyncKeyDown. It is called
	// on the loop goroutine, only with HeldFile.
	KeyDown func(KeyEvent) bool
	// Identity returns the logon session of this run, which the log's
	// header keeps: the sweep takes the keys of a log only of the same,
	// every field equal (SweepResult.Stale). nil means
	// SystemHeldIdentity. It is called once, on the loop goroutine, before
	// the log is opened, only with HeldFile; when it fails, the run keeps
	// no log (Off).
	Identity func() (HeldIdentity, error)
}

// SweepResult is what the startup sweep of the held-key log did
// (Loop.Swept).
type SweepResult struct {
	// Released are the releases the sweep sent: the keys of the log that
	// KeyDown reported down, and last the menu mask key's when KeyDown
	// reported it down.
	Released []KeyEvent
	// Err is why the sweep did not release every such key, or why the log
	// was reset (Reset, Stale). The keys not released stay in the log: the
	// next closing BlockFor, of the pause, of the system's suspension or of
	// padmatectl release-all, and Stop release those KeyDown reports down
	// then, or the next start tries again. It joins ErrLoopClosed and the
	// *PanicError when the loop panicked during the sweep.
	Err error
	// Reset reports that the log was corrupt, with Err saying why: the
	// sweep reset it to no key, so the keys the last run held, if any,
	// are not known, and none of them is released or kept.
	Reset bool
	// Stale reports that the log is of another boot or logon session than
	// this run (LoopOptions.Identity), StaleFrom: the sweep reset it to no
	// key and released none of the keys it had, StaleKeys (as releases),
	// which the end of that session let go of; a key down now is someone
	// else's. It still releases the menu mask key when KeyDown reports it
	// down, as with no log.
	Stale     bool
	StaleFrom HeldIdentity
	StaleKeys []KeyEvent
	// Off reports that the log could not be opened, created or written,
	// or that Identity failed, with Err saying why (ErrHeldInUse when
	// another process has it open): this run keeps no log, and the sweep
	// released nothing.
	Off bool
}

// Loop is T-output: one goroutine that owns an Arbiter, runs the commands
// other goroutines hand it with Do one at a time, and runs the Arbiter's
// deadlines on one timer. The Arbiter is never touched outside it.
//
// Stop and a panic on the loop goroutine both let go of every key
// (CLAUDE.md invariant 1). Stop releases through the Arbiter. After a
// panic the Arbiter's state is suspect, so the loop releases the keys a
// ledger saw the Sender inject, and ends. The ledger's Sender is a
// MenuMask over the Loop's Sender (OUT-08, ADR-0021).
//
// With LoopOptions.HeldFile the loop keeps the held-key log (OUT-14,
// ADR-0027): it sweeps what the last run left in it before its first
// command (Swept), the ledger writes it before and after every Send, and
// Stop or the release after a panic finish it.
//
// BlockFor releases every key and closes the Arbiter's gate on presses
// (Arbiter.SetBlocked, DMN-06) for one owner without waiting for the loop,
// so that T-input and the system's session events can pause the output
// while the loop is busy. The gate stays closed while any owner keeps it
// closed (DMN-09); Block is BlockFor for BlockUser.
type Loop struct {
	clk    clock.Clock
	errors func(error)
	cmds   chan command
	stop   chan struct{}
	once   sync.Once
	done   chan struct{}

	// gate is what BlockFor asks of the loop: bits 0-7 are the owners that
	// keep the Arbiter's gate closed (Blocker bits), and the bits from
	// gateCount up count the closing calls, each asking for a ReleaseAll.
	// The loop applies it when it gets a token from wake (capacity 1) and
	// before every command. The count is apart from the owners so that a
	// release can also be asked for on an owner already closed.
	gate atomic.Uint64
	wake chan struct{}

	// Written by the loop goroutine before done is closed.
	stopErr error
	err     *PanicError

	// swept is closed once the startup sweep is done, with sweep set.
	swept chan struct{}
	sweep SweepResult
}

type command struct {
	f     func(*Arbiter) error
	reply chan error // buffered, so the loop never waits for the caller
	// mark is the input edge the command answers (DMN-15), or nil. It points into
	// the RemoteArbiter of the slot that waits for the reply.
	mark *InputMark
}

// StartLoop starts a Loop that owns a new Arbiter on clk, injecting
// through s. It panics if clk or s is nil.
func StartLoop(clk clock.Clock, s Sender, opts LoopOptions) *Loop {
	if clk == nil || s == nil {
		panic("output: StartLoop needs a clock and a sender")
	}
	l := &Loop{
		clk:    clk,
		errors: opts.Errors,
		cmds:   make(chan command),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		wake:   make(chan struct{}, 1),
		swept:  make(chan struct{}),
	}
	if opts.HeldFile == "" {
		close(l.swept) // nothing to sweep
	}
	go l.run(s, opts)
	return l
}

// Swept waits for the startup sweep of the held-key log and returns what
// it did. It returns the zero SweepResult at once without
// LoopOptions.HeldFile.
//
// Before the loop runs its first command, it opens LoopOptions.HeldFile,
// takes the keys the last run left in it when that run was of the same
// logon session (LoopOptions.Identity; otherwise it resets the
// log, Stale), and sends the releases of those that KeyDown reports down,
// through the Loop's MenuMask, with no gate:
// on T-output, like every other Send (CLAUDE.md invariant 3). When KeyDown
// reports the menu mask key down, which the log never has, it releases it
// too: with the others, or alone. The Do calls wait for it; BlockFor
// calls made meanwhile are applied after it. Its errors go to the result
// only; the loop runs on whatever it did.
func (l *Loop) Swept() SweepResult {
	<-l.swept
	return l.sweep
}

// Do runs f on the loop goroutine and returns its error. The loop first
// applies the BlockFor calls made so far, then runs the work that is due
// (Advance when NextDeadline is not after Now, its error to
// LoopOptions.Errors). Commands from many goroutines run one at a time, in
// arrival order. f may call Block, BlockFor and Blocking but no other
// method of the Loop, which could deadlock, and must not keep the Arbiter.
//
// Once the loop has ended, Do returns ErrLoopClosed without running f; a
// Do racing with Stop runs before the release or not at all. When f, or
// the loop's work before it, panics, the error wraps ErrLoopClosed and
// the *PanicError.
func (l *Loop) Do(f func(*Arbiter) error) error { return l.do(f, nil) }

// do is Do for a command that answers the input edge mark (DMN-15), nil for
// none.
func (l *Loop) do(f func(*Arbiter) error, mark *InputMark) error {
	if f == nil {
		return errors.New("output: Do needs a function")
	}
	c := command{f: f, reply: make(chan error, 1), mark: mark}
	select {
	case l.cmds <- c:
	case <-l.stop:
		return ErrLoopClosed
	case <-l.done:
		return ErrLoopClosed
	}
	return <-c.reply
}

// Blocker names who closes the Arbiter's gate through Loop.BlockFor. The
// gate is closed while at least one owner keeps it closed, so one owner's
// open never undoes another's close.
type Blocker uint8

const (
	// BlockUser is the pause (DMN-06, DMN-07); Block uses it.
	BlockUser Blocker = 1 << iota
	// BlockSystem is the system's suspension of the mapping (DMN-09):
	// lock, session disconnect, sleep, the end of the session and the
	// secure desktop.
	BlockSystem
	// BlockEmergency fences a padmatectl release-all while every slot drops
	// its script timers and owner references.
	BlockEmergency
	// BlockResume fences SDL reenumeration and slot state reconciliation
	// after wake, independently of pause and session suspension.
	BlockResume
	// BlockCapture belongs to input-capture leases, independent of user pause.
	BlockCapture
	// BlockOwnWindow fences local dry-run transitions while slots cancel
	// their old input cycles and script runtimes.
	BlockOwnWindow
	// BlockExit is the tray's or the installer's exit (DMN-22, PKG-04):
	// padmated lets go of every key at once and presses nothing more while
	// it waits for the UI to save, before it exits. Nothing opens it.
	BlockExit

	allBlockers = BlockUser | BlockSystem | BlockEmergency | BlockResume | BlockCapture | BlockOwnWindow | BlockExit
)

// gateCount is the gate's lowest counting bit: the bits below it hold the
// closed owners.
const gateCount = 1 << 8

// Block is BlockFor(BlockUser, on).
func (l *Loop) Block(on bool) { l.BlockFor(BlockUser, on) }

// BlockFor with on true asks the loop to run ReleaseAll, through the
// MenuMask like any command, then to release the keys of the held-key log
// that the startup sweep could not release (Swept) and that KeyDown
// reports down, as Stop does, and then to close the Arbiter's gate on
// presses (Arbiter.SetBlocked) for the owners in b; the releases' errors
// go to LoopOptions.Errors. With on false it asks the loop to take b's
// owners off the gate, which opens once no owner keeps it closed. Bits of
// b that name no Blocker are ignored; with none left, BlockFor does
// nothing.
//
// BlockFor never waits for the loop: it may be called from any goroutine,
// from inside a Do function too, and does nothing once the loop has ended.
// A Do that starts after BlockFor returns runs after its release, with the
// gate as that call or a later one left it. Calls the loop has not applied
// yet coalesce: the gate ends as the owners were last left, and the loop
// runs one ReleaseAll if any of the calls had on true. A closing call on
// an owner already closed still runs a ReleaseAll, which resends the
// releases still pending. Stop's release does not depend on the gate.
func (l *Loop) BlockFor(b Blocker, on bool) {
	b &= allBlockers
	if b == 0 {
		return
	}
	for {
		old := l.gate.Load()
		g := old &^ uint64(b)
		if on {
			g = (old + gateCount) | uint64(b)
		}
		if l.gate.CompareAndSwap(old, g) {
			break
		}
	}
	select {
	case l.wake <- struct{}{}:
	default: // a token is waiting: the loop will load the new gate
	}
}

// Blocking returns the owners that the BlockFor calls so far keep on the
// gate: what the callers asked, which the loop may not have applied yet.
func (l *Loop) Blocking() Blocker { return Blocker(l.gate.Load() % gateCount) }

// Remote returns a RemoteArbiter that sends its commands to l.
func (l *Loop) Remote() *RemoteArbiter { return &RemoteArbiter{l: l} }

// Stop ends the loop and returns once it has ended. The loop runs
// ReleaseAll, then, while releases are pending, up to 3 retries at the
// Arbiter's NextDeadline on the clock. Stop returns nil when every release
// went out in the end, and the failures on the way go to
// LoopOptions.Errors; otherwise it returns the joined errors of the
// release and the retries, which name the keys that may be held. After a
// panic it returns Err. Stop may be called more than once and from any
// goroutine, but not from inside a Do function.
func (l *Loop) Stop() error {
	l.once.Do(func() { close(l.stop) })
	<-l.done
	if l.err != nil {
		return l.err
	}
	return l.stopErr
}

// Done is closed when the loop has ended, by Stop or by a panic.
func (l *Loop) Done() <-chan struct{} { return l.done }

// Err returns the *PanicError that ended the loop, or nil while it runs
// and after Stop.
func (l *Loop) Err() error {
	select {
	case <-l.done:
		if l.err != nil {
			return l.err
		}
	default:
	}
	return nil
}

func (l *Loop) run(s Sender, opts LoopOptions) {
	if opts.Lock {
		runtime.LockOSThread() // never unlocked: the thread exits with the loop
	}
	led := &ledger{s: NewMenuMask(s), report: l.report, keyDown: opts.KeyDown}
	if led.keyDown == nil {
		led.keyDown = AsyncKeyDown
	}
	a := NewArbiter(l.clk, led, opts.Arbiter)
	alarm := clock.NewAlarm(l.clk)
	var cur *command // the command in flight
	ended := false
	swept := opts.HeldFile == ""
	defer func() {
		if !ended {
			l.err = panicked(recover(), led, alarm)
		}
		if !swept {
			l.sweep = SweepResult{Err: errors.Join(ErrLoopClosed, l.err)}
			close(l.swept)
		}
		// Done first: when Do returns the panic, Err reports it.
		close(l.done)
		if cur != nil && l.err != nil {
			cur.reply <- errors.Join(ErrLoopClosed, l.err)
		}
	}()

	if !swept {
		l.sweep = sweepHeld(opts.HeldFile, opts.Identity, led)
		swept = true
		close(l.swept)
	}
	due := func() {
		if d, ok := a.NextDeadline(); ok && d <= l.clk.Now() {
			l.report(a.Advance())
		}
	}
	var gate uint64 // the last gate applied
	applyGate := func() {
		g := l.gate.Load()
		if g/gateCount != gate/gateCount {
			l.report(a.ReleaseAll())
			l.report(led.releaseCarried())
		}
		a.SetBlocked(g%gateCount != 0)
		gate = g
	}
	for {
		select {
		case c := <-l.cmds:
			cur = &c
			if opts.Guard != nil {
				opts.Guard(a)
			}
			applyGate()
			due()
			// After the due work and the gate, so that their releases cannot end the
			// measurement of the edge (DMN-15).
			a.mark = c.mark
			err := c.f(a)
			a.mark = nil
			// Re-armed before the reply, so the deadlines of the command are
			// on the timer when Do returns.
			alarm.Set(a.NextDeadline())
			cur = nil
			c.reply <- err
		case <-alarm.C():
			alarm.Fired()
			if opts.Guard != nil {
				opts.Guard(a)
			}
			applyGate()
			due()
			alarm.Set(a.NextDeadline())
		case <-l.wake:
			if opts.Guard != nil {
				opts.Guard(a)
			}
			applyGate()
			alarm.Set(a.NextDeadline())
		case <-l.stop:
			l.stopErr = l.shutdown(a, alarm, led)
			ended = true
			return
		}
	}
}

func (l *Loop) report(err error) {
	if err != nil && l.errors != nil {
		l.errors(err)
	}
}

// sweepHeld opens the held-key log at path for the ledger, for the logon
// session identity returns (nil: SystemHeldIdentity), and runs
// the startup sweep (Loop.Swept) through the ledger's MenuMask.
func sweepHeld(path string, identity func() (HeldIdentity, error), led *ledger) SweepResult {
	if identity == nil {
		identity = SystemHeldIdentity
	}
	id, err := identity()
	if err != nil {
		return SweepResult{Off: true, Err: fmt.Errorf("output: the logon session of the held-key log: %w", err)}
	}
	h, from, prev, err := openHeldLog(path, id)
	if h == nil {
		return SweepResult{Off: true, Err: err}
	}
	led.held = h
	res := SweepResult{Err: err, Reset: errors.Is(err, errHeldCorrupt)}
	if !from.same(id) {
		// Reset, not swept: the next write has this run's header and no
		// key of the other's.
		res.Stale, res.StaleFrom, res.StaleKeys = true, from, prev.keys()
		prev = heldBits{}
	}
	// The keys of the last run are carried until KeyDown reports them up
	// or their release goes out, so that a panic on the way leaves them in
	// the log.
	h.carried = prev
	var active []KeyEvent
	for _, e := range prev.keys() {
		if led.keyDown(e) {
			active = append(active, e)
		} else {
			h.carried.clear(e.ID())
		}
	}
	// The MenuMask releases the mask key first in its next batch.
	mask := led.keyDown(MenuMaskEvent(false))
	if mask {
		led.s.maskDown = true
	}
	n, sendErr := 0, error(nil)
	if len(active) > 0 {
		n, sendErr = send(led.s, active)
	} else {
		sendErr = led.s.releaseMask()
	}
	if sendErr != nil {
		msg := "releasing the keys the last run held"
		if n < len(active) {
			msg = "releasing " + names(active[n:]) + ", which the last run held, failed"
		}
		res.Err = errors.Join(res.Err, fmt.Errorf("output: %s: %w", msg, sendErr))
	}
	for _, e := range active[:n] {
		h.carried.clear(e.ID())
	}
	res.Released = active[:n:n]
	if mask && !led.s.maskDown {
		res.Released = append(res.Released, MenuMaskEvent(true))
	}
	// The log keeps the keys carried only: the others are up.
	if err := h.settle(nil, nil); err != nil {
		res.Err = errors.Join(res.Err, err)
	}
	return res
}

// shutdown releases everything and retries the pending releases, waiting
// on alarm for each retry, then releases the carried keys of the held-key
// log that are down and finishes the log. When no release is left
// pending, it reports the failures on the way and returns nil. The
// errors of the log go to Errors.
func (l *Loop) shutdown(a *Arbiter, alarm *clock.Alarm, led *ledger) error {
	errs := []error{a.ReleaseAll()}
	for range stopRetries {
		d, ok := a.NextDeadline()
		if a.Stats().PendingReleases == 0 || !ok {
			break
		}
		alarm.Set(d, true)
		<-alarm.C()
		alarm.Fired()
		errs = append(errs, a.Advance())
	}
	alarm.Stop()
	err := errors.Join(errs...)
	heldErr := led.releaseCarried()
	heldErr = errors.Join(heldErr, led.held.finish(led.down))
	l.report(heldErr)
	if a.Stats().PendingReleases == 0 {
		l.report(err)
		return nil
	}
	return err
}

// panicked handles what recover returned on the loop goroutine: nil means
// the goroutine is exiting by runtime.Goexit. It releases the ledger's
// keys, then the carried keys of the held-key log that KeyDown reports
// down, as Stop does, since padmated exits after a panic (CLAUDE.md
// invariant 1); finishes the log with the keys still down and the carried
// ones left; and stops the alarm. The log's error goes to HeldErr, not to
// Errors, which may be what panicked.
func panicked(v any, led *ledger, alarm *clock.Alarm) *PanicError {
	if v == nil {
		v = "runtime.Goexit"
	}
	pe := &PanicError{Value: v, Stack: debug.Stack()}
	pe.Released, pe.ReleaseErr = led.release()
	carried, err := led.releaseCarriedAfterPanic()
	pe.Released = append(pe.Released, carried...)
	pe.ReleaseErr = errors.Join(pe.ReleaseErr, err)
	func() {
		defer func() {
			if v := recover(); v != nil {
				pe.HeldErr = fmt.Errorf("output: panic finishing the held-key log: %v", v)
			}
		}()
		pe.HeldErr = led.held.finish(led.down)
	}()
	func() {
		defer func() { _ = recover() }() // the clock may be what panicked
		alarm.Stop()
	}()
	return pe
}

// PanicError is the error of a Loop that ended by a panic on its
// goroutine.
type PanicError struct {
	Value any    // what was recovered
	Stack []byte // debug.Stack() at the recover
	// Released are the releases sent after the panic: those of the keys
	// the ledger saw go down, then those of the held-key log's keys that
	// the startup sweep could not release and KeyDown reports down (OUT-14).
	// ReleaseErr is the error of sending them (a Send error or a second
	// panic, in KeyDown too), which names the keys that may be held.
	// Released never has the mask key (OUT-08); ReleaseErr names it when
	// it may be left down.
	Released   []KeyEvent
	ReleaseErr error
	// HeldErr is the error of finishing the held-key log after the release
	// (LoopOptions.HeldFile): the file stays with the keys that may be held.
	HeldErr error
}

func (e *PanicError) Error() string {
	msg := fmt.Sprintf("output: panic on the T-output loop: %v", e.Value)
	if e.ReleaseErr != nil {
		msg += "; releasing the keys after it: " + e.ReleaseErr.Error()
	}
	if e.HeldErr != nil {
		msg += "; held-key log: " + e.HeldErr.Error()
	}
	return msg
}

// ledger is the Sender of a Loop's Arbiter. It forwards every call to the
// MenuMask unchanged and keeps the presses whose key the injected events
// left down, so that the keys can be released without the Arbiter. With a
// held-key log it marks the presses of each batch in the log before the
// MenuMask gets the batch, and settles the log to the keys down after
// (OUT-14); the log's errors go to report, and the batch goes out anyway.
// After a panic in the Sender it does not settle: the log keeps the
// batch's presses, which the ledger counts as down.
type ledger struct {
	s       *MenuMask
	down    []KeyEvent // in press order
	held    *heldLog   // nil without a log
	report  func(error)
	keyDown func(KeyEvent) bool
}

func (l *ledger) Send(events []KeyEvent) (n int, err error) {
	if l.held != nil {
		if err := l.held.mark(events); err != nil {
			l.report(err)
		}
	}
	ok := false
	defer func() {
		if !ok {
			// The Send panicked and may have injected any of the events: its
			// presses count as down.
			for _, e := range events {
				if !e.Up && !e.Pointer() {
					l.press(e)
				}
			}
		}
	}()
	n, err = l.s.Send(events)
	ok = true
	sent := events[:min(max(n, 0), len(events))]
	for _, e := range sent {
		switch {
		case e.Pointer():
			// A move has no release, so there is nothing for the ledger to let go
			// of.
		case e.Up:
			l.down = slices.DeleteFunc(l.down, e.sameKey)
		default:
			l.press(e)
		}
	}
	if l.held != nil {
		if err := l.held.settle(l.down, sent); err != nil {
			l.report(err)
		}
	}
	return n, err
}

func (l *ledger) press(e KeyEvent) {
	if !slices.ContainsFunc(l.down, e.sameKey) {
		l.down = append(l.down, e)
	}
}

// release sends the releases of the keys the ledger has down, in reverse
// press order, in one Send call on the MenuMask, so a lone Alt or Win
// release is masked and a mask left down is released first. With no key
// down it still releases a mask left down. It returns the releases that
// went out, never the mask's, and takes their keys off down; a panic in
// the call is recovered into the error.
func (l *ledger) release() (released []KeyEvent, err error) {
	ups := make([]KeyEvent, len(l.down))
	for i, d := range l.down {
		ups[len(ups)-1-i] = d.release()
	}
	held := names(ups)
	if len(ups) == 0 {
		held = MenuMaskEvent(false).name()
	}
	defer func() {
		if v := recover(); v != nil {
			if len(ups) > 0 && l.s.maskDown {
				held += ", " + MenuMaskEvent(false).name()
			}
			released, err = nil, fmt.Errorf("output: panic releasing %s, the keys may be held: %v", held, v)
		}
	}()
	if len(ups) == 0 {
		return nil, l.s.releaseMask()
	}
	n, err := send(l.s, ups)
	for _, e := range ups[:n] {
		l.down = slices.DeleteFunc(l.down, e.sameKey)
	}
	if n < len(ups) {
		err = fmt.Errorf("output: releasing %s failed, the keys may be held: %w", names(ups[n:]), err)
	}
	return ups[:n], err
}

// releaseCarried sends, in one Send call through the ledger, the releases
// of the keys that the startup sweep could not release and that keyDown
// reports down: the ledger takes those that went out off the held-key
// log's carried keys, and settles the log. The others stay carried, for
// the next ReleaseAll of BlockFor, Stop or the next start. The log's
// errors go to report, as for every Send of the ledger.
func (l *ledger) releaseCarried() error {
	if l.held == nil || l.held.f == nil {
		return nil
	}
	var ups []KeyEvent
	for _, e := range l.held.carried.keys() {
		if l.keyDown(e) {
			ups = append(ups, e)
		}
	}
	n, err := send(l, ups)
	if n < len(ups) {
		return fmt.Errorf("output: releasing %s, which the last run held, failed: %w", names(ups[n:]), err)
	}
	return err
}

// releaseCarriedAfterPanic is releaseCarried after a panic on the loop
// goroutine: it sends the releases in one Send call on the MenuMask, not
// through the ledger, since report may be what panicked, and takes those
// that went out off the carried keys, without writing the log, which
// finish does next. It returns the releases that went out; a panic in
// keyDown or in the call is recovered into the error, which then names
// the carried keys not released, and the mask key when it may be left
// down.
func (l *ledger) releaseCarriedAfterPanic() (released []KeyEvent, err error) {
	if l.held == nil || l.held.f == nil {
		return nil, nil
	}
	ups := l.held.carried.keys()
	if len(ups) == 0 {
		return nil, nil
	}
	held := names(ups) // until keyDown has picked the keys down
	defer func() {
		if v := recover(); v != nil {
			if l.s.maskDown {
				held += ", " + MenuMaskEvent(false).name()
			}
			released, err = nil, fmt.Errorf("output: panic releasing %s, which the last run held, the keys may be held: %v", held, v)
		}
	}()
	ups = slices.DeleteFunc(ups, func(e KeyEvent) bool { return !l.keyDown(e) })
	held = names(ups)
	n, err := send(l.s, ups)
	for _, e := range ups[:n] {
		l.held.carried.clear(e.ID())
	}
	if n < len(ups) {
		err = fmt.Errorf("output: releasing %s, which the last run held, failed, the keys may be held: %w", names(ups[n:]), err)
	}
	return ups[:n:n], err
}

// RemoteArbiter sends each command to a Loop with Do. It satisfies
// slot.Arbiter. Advance does nothing and NextDeadline reports none: the
// Loop runs the Arbiter's deadlines itself.
type RemoteArbiter struct {
	l *Loop
	// mark is the input edge that the slot is handling (MarkInput, DMN-15).
	mark InputMark
}

// Down runs Arbiter.Down on the loop.
func (r *RemoteArbiter) Down(o Owner, c keys.Chord) error {
	return r.do(func(a *Arbiter) error { return a.Down(o, c) })
}

// DownFor runs Arbiter.DownFor on the output loop.
func (r *RemoteArbiter) DownFor(o Owner, c keys.Chord, minPress time.Duration) error {
	return r.do(func(a *Arbiter) error { return a.DownFor(o, c, minPress) })
}

// TapSequence runs Arbiter.TapSequence on the output loop.
func (r *RemoteArbiter) TapSequence(o Owner, items []keys.Chord, hold, gap time.Duration) error {
	return r.do(func(a *Arbiter) error { return a.TapSequence(o, items, hold, gap) })
}

// Sequence runs Arbiter.Sequence on the output loop (MAP-22). The command
// returns once the sequence is queued and its first steps are run; the loop runs
// the rest at the Arbiter's deadlines.
func (r *RemoteArbiter) Sequence(o Owner, steps []keys.Step, hold, gap time.Duration) error {
	return r.do(func(a *Arbiter) error { return a.Sequence(o, steps, hold, gap) })
}

// SequenceWithCompletion runs Arbiter.SequenceWithCompletion on the output loop.
// The callback runs on T-output when the sequence ends, once, and must only
// transfer data to its owner; it must not call the Loop. When the loop has ended
// it runs with Dropped and Canceled, as TapWithCompletion's does.
func (r *RemoteArbiter) SequenceWithCompletion(o Owner, steps []keys.Step, hold, gap time.Duration, done func(TapCompletion)) error {
	var once sync.Once
	complete := func(result TapCompletion) {
		if done != nil {
			once.Do(func() { done(result) })
		}
	}
	err := r.do(func(a *Arbiter) error { return a.SequenceWithCompletion(o, steps, hold, gap, complete) })
	if errors.Is(err, ErrLoopClosed) {
		complete(TapCompletion{Dropped: true, Canceled: true})
	}
	return err
}

// TypeText runs Arbiter.TypeText on the output loop (OUT-21). The command
// returns once the text is queued and its first character typed; the loop types
// the rest at the Arbiter's deadlines.
func (r *RemoteArbiter) TypeText(o Owner, text string, gap time.Duration) error {
	return r.do(func(a *Arbiter) error { return a.TypeText(o, text, gap) })
}

// TypeTextWithCompletion runs Arbiter.TypeTextWithCompletion on the output loop.
// The callback runs on T-output when the text ends, once, and must only transfer
// data to its owner; it must not call the Loop. When the loop has ended it runs
// with Dropped and Canceled, as TapWithCompletion's does.
func (r *RemoteArbiter) TypeTextWithCompletion(o Owner, text string, gap time.Duration, done func(TapCompletion)) error {
	var once sync.Once
	complete := func(result TapCompletion) {
		if done != nil {
			once.Do(func() { done(result) })
		}
	}
	err := r.do(func(a *Arbiter) error { return a.TypeTextWithCompletion(o, text, gap, complete) })
	if errors.Is(err, ErrLoopClosed) {
		complete(TapCompletion{Dropped: true, Canceled: true})
	}
	return err
}

// RepeatTap runs Arbiter.RepeatTap on the output loop.
func (r *RemoteArbiter) RepeatTap(o Owner, c keys.Chord, hold time.Duration) error {
	return r.l.Do(func(a *Arbiter) error { return a.RepeatTap(o, c, hold) })
}

// StopRepeat runs Arbiter.StopRepeat even with the press gate closed.
func (r *RemoteArbiter) StopRepeat(o Owner, c keys.Chord) error {
	return r.l.Do(func(a *Arbiter) error { return a.StopRepeat(o, c) })
}

// Move runs Arbiter.Move on the loop.
func (r *RemoteArbiter) Move(o Owner, dx, dy int32) error {
	return r.l.Do(func(a *Arbiter) error { return a.Move(o, dx, dy) })
}

// Jump runs Arbiter.Jump on the loop.
func (r *RemoteArbiter) Jump(o Owner, x, y int32) error {
	return r.l.Do(func(a *Arbiter) error { return a.Jump(o, x, y) })
}

// Up runs Arbiter.Up on the loop.
func (r *RemoteArbiter) Up(o Owner, c keys.Chord) error {
	return r.do(func(a *Arbiter) error { return a.Up(o, c) })
}

// Tap runs Arbiter.Tap on the loop.
func (r *RemoteArbiter) Tap(o Owner, c keys.Chord, hold time.Duration) error {
	return r.do(func(a *Arbiter) error { return a.Tap(o, c, hold) })
}

// TapWithCompletion runs the callback on T-output after the tap ends. The
// callback must only transfer data to its owner; it must not call the Loop.
func (r *RemoteArbiter) TapWithCompletion(o Owner, c keys.Chord, hold time.Duration, done func(TapCompletion)) error {
	var once sync.Once
	complete := func(result TapCompletion) {
		if done != nil {
			once.Do(func() { done(result) })
		}
	}
	err := r.do(func(a *Arbiter) error { return a.TapWithCompletion(o, c, hold, complete) })
	if errors.Is(err, ErrLoopClosed) {
		complete(TapCompletion{Dropped: true, Canceled: true})
	}
	return err
}

// ReleaseOwner releases the exact owner on the output loop. It is the
// single-owner counterpart used by script runtimes during teardown and
// reload, and keeps RemoteArbiter's command surface aligned with Arbiter.
func (r *RemoteArbiter) ReleaseOwner(o Owner) error {
	return r.l.Do(func(a *Arbiter) error { return a.ReleaseOwner(o) })
}

// Repeat runs Arbiter.Repeat on the loop.
func (r *RemoteArbiter) Repeat(o Owner, c keys.Chord) error {
	return r.l.Do(func(a *Arbiter) error { return a.Repeat(o, c) })
}

// ReleaseOwners runs Arbiter.ReleaseOwners on the loop; match runs on the
// loop goroutine.
func (r *RemoteArbiter) ReleaseOwners(match func(Owner) bool) error {
	return r.l.Do(func(a *Arbiter) error { return a.ReleaseOwners(match) })
}

// Advance does nothing: the Loop runs the deadlines.
func (*RemoteArbiter) Advance() error { return nil }

// NextDeadline reports none: the Loop runs the deadlines.
func (*RemoteArbiter) NextDeadline() (clock.Time, bool) { return 0, false }
