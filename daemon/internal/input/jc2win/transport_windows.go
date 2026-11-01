package jc2win

// transport_windows.go: the Transport, its goroutine and its queue of
// requests. The goroutine owns every WinRT object that is not touched from a
// callback; the API methods only queue requests for it.

import (
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
)

// How long the goroutine waits for a consumer that does not read Events
// before it drops a link event.
const emitWait = 2 * time.Second

// Transport is the Windows Bluetooth LE transport. Its methods are safe for
// concurrent use and never block on Bluetooth.
type Transport struct {
	log    *slog.Logger
	opts   Options
	events chan Event

	dropped atomic.Uint64

	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	closing   atomic.Bool
	wake      chan struct{} // capacity 1: something for the goroutine to look at

	// qmu guards queue and qclosed. It is held only to append or remove an
	// element, never while anything slow runs, so callbacks may take it.
	qmu     sync.Mutex
	queue   []command
	qclosed bool

	mu     sync.Mutex // guards links and nextID
	links  map[LinkID]*link
	nextID LinkID

	scanWant atomic.Bool
	// Scan state. scan is owned by the goroutine; the scanner's callback
	// sets scanAbort when Windows stopped the watcher on its own.
	scan        *scanner
	scanAbort   atomic.Int32 // a bluetooth.BluetoothError code, 0 none
	scanRetryAt time.Time
	scanFailed  bool
}

type cmdKind uint8

const (
	cmdConnect    cmdKind = iota + 1 // open and set up l
	cmdDisconnect                    // end l on request
	cmdRelease                       // l was reported down by a callback; free it
)

type command struct {
	kind cmdKind
	link *link
}

// Start starts the transport's goroutine and returns once the Windows Runtime
// is up on it. It does not scan or connect until asked.
func Start(opts Options) (*Transport, error) {
	n := opts.Events
	if n <= 0 {
		n = DefaultEvents
	}
	lg := opts.Log
	if lg == nil {
		lg = slog.New(slog.DiscardHandler)
	}
	// latency.Now looks up QueryPerformanceCounter on its first call; make
	// that call here, so that the callbacks only read the counter.
	latency.Now()

	t := &Transport{
		log:    lg,
		opts:   opts,
		events: make(chan Event, n),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		wake:   make(chan struct{}, 1),
		links:  make(map[LinkID]*link),
	}
	ready := make(chan error, 1)
	go t.run(ready)
	if err := <-ready; err != nil {
		<-t.done
		return nil, err
	}
	return t, nil
}

// Events returns the channel of everything the transport saw. It is never
// closed, not even by Close.
func (t *Transport) Events() <-chan Event { return t.events }

// Dropped returns the number of events lost because the channel was full.
func (t *Transport) Dropped() uint64 { return t.dropped.Load() }

// Scan turns passive scanning for Nintendo's advertisements on or off. Each
// Advert event is one received advertisement. It is idempotent; if Windows
// stops the scan by itself (the radio was switched off), the transport tries
// again every few seconds until Scan(false).
func (t *Transport) Scan(on bool) {
	t.scanWant.Store(on)
	t.signal()
}

// Connect starts connecting to the controller at addr, whose advertisement
// said it is side, and lights player on its lights (0..7). It returns at once
// with the id of the new link; the result is a Connected or a Failed event
// with that id. It returns 0 after Close.
func (t *Transport) Connect(addr uint64, side jc2.Side, player int) LinkID {
	if t.closing.Load() {
		return 0
	}
	t.mu.Lock()
	t.nextID++
	l := newLink(t.nextID, addr, side, player, t.log)
	t.links[l.id] = l
	t.mu.Unlock()
	if !t.post(command{kind: cmdConnect, link: l}) {
		t.mu.Lock()
		delete(t.links, l.id)
		t.mu.Unlock()
		return 0
	}
	return l.id
}

// Disconnect ends a link: a link that is still connecting ends in Failed
// (ErrCanceled), a connected one in Lost. The Windows Runtime objects are
// closed, so Windows drops the connection. An unknown or finished link is
// ignored.
func (t *Transport) Disconnect(id LinkID) {
	t.mu.Lock()
	l := t.links[id]
	t.mu.Unlock()
	if l == nil {
		return
	}
	l.cancelNow()
	t.post(command{kind: cmdDisconnect, link: l})
}

// Close stops scanning, disconnects every link and waits for the goroutine to
// finish. A Lost event is queued, best effort, for each connected link. It is
// idempotent.
func (t *Transport) Close() {
	t.closeOnce.Do(func() {
		t.closing.Store(true)
		close(t.stop)
	})
	<-t.done
}

// signal wakes the goroutine.
func (t *Transport) signal() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// post queues c for the goroutine; false once the queue is closed.
func (t *Transport) post(c command) bool {
	t.qmu.Lock()
	if t.qclosed {
		t.qmu.Unlock()
		return false
	}
	t.queue = append(t.queue, c)
	t.qmu.Unlock()
	t.signal()
	return true
}

func (t *Transport) pop() (command, bool) {
	t.qmu.Lock()
	defer t.qmu.Unlock()
	if len(t.queue) == 0 {
		return command{}, false
	}
	c := t.queue[0]
	t.queue[0] = command{}
	t.queue = t.queue[1:]
	return c, true
}

// emit sends ev without blocking and counts it as dropped if the channel is
// full. It is what callbacks use.
func (t *Transport) emit(ev Event) {
	select {
	case t.events <- ev:
	default:
		t.dropped.Add(1)
	}
}

// trySend sends ev without blocking and says whether it went.
func (t *Transport) trySend(ev Event) bool {
	select {
	case t.events <- ev:
		return true
	default:
		return false
	}
}

// emitWait sends ev from the goroutine: it waits for room, but not forever
// and not once the transport is closing. It says whether ev went.
func (t *Transport) emitWait(ev Event) bool {
	if t.trySend(ev) {
		return true
	}
	timer := time.NewTimer(emitWait)
	defer timer.Stop()
	select {
	case t.events <- ev:
		return true
	case <-t.stop:
	case <-timer.C:
	}
	t.dropped.Add(1)
	return false
}

// run is the transport's goroutine. It is locked to its OS thread for good:
// the thread ends with the goroutine, which also ends the apartment.
func (t *Transport) run(ready chan<- error) {
	runtime.LockOSThread()
	defer close(t.done)
	if err := roInitialize(); err != nil {
		ready <- err
		return
	}
	defer roUninitialize()
	ready <- nil
	t.loop()
	t.shutdown()
}

func (t *Transport) loop() {
	for {
		select {
		case <-t.stop:
			return
		default:
		}
		t.reconcileScan()
		c, ok := t.pop()
		if !ok {
			var retry <-chan time.Time
			if t.scanWant.Load() && t.scan == nil && !t.scanRetryAt.IsZero() {
				retry = time.After(time.Until(t.scanRetryAt))
			}
			select {
			case <-t.wake:
			case <-t.stop:
				return
			case <-retry:
			}
			continue
		}
		switch c.kind {
		case cmdConnect:
			t.doConnect(c.link)
		case cmdDisconnect:
			t.finish(c.link, nil)
		case cmdRelease:
			t.finish(c.link, nil)
		}
	}
}

// shutdown ends everything. It runs on the goroutine after the loop.
func (t *Transport) shutdown() {
	t.scanWant.Store(false)
	t.stopScan()

	t.qmu.Lock()
	pending := t.queue
	t.queue = nil
	t.qclosed = true
	t.qmu.Unlock()
	for _, c := range pending {
		if c.link != nil {
			c.link.cancelNow()
		}
	}

	t.mu.Lock()
	links := make([]*link, 0, len(t.links))
	for _, l := range t.links {
		links = append(links, l)
	}
	t.mu.Unlock()
	for _, l := range links {
		l.cancelNow()
		t.finishWith(l, ErrCanceled, false)
	}
}

// finish ends l on request or after a callback reported it down.
func (t *Transport) finish(l *link, cause error) { t.finishWith(l, cause, true) }

// finishWith gives l its one terminal event and closes everything it holds:
// Lost if it was live, Failed if it never was. A link that a callback
// already ended has had its Lost sent; it is sent here only if the channel
// was full then. wait says whether a full channel may be waited on.
func (t *Transport) finishWith(l *link, cause error, wait bool) {
	if l.finished {
		return
	}
	l.finished = true
	old := l.state.Swap(stEnded)
	start := time.Now()
	l.release()
	t.mu.Lock()
	delete(t.links, l.id)
	t.mu.Unlock()

	send := t.trySend
	if wait {
		send = t.emitWait
	}
	switch {
	case old == stLive:
		send(Event{Kind: Lost, Link: l.id, Addr: l.addr, Err: nil})
		l.log.Info("link closed", "release", time.Since(start).Round(time.Millisecond))
	case old == stConnecting:
		if cause == nil {
			cause = ErrCanceled
		}
		send(Event{Kind: Failed, Link: l.id, Addr: l.addr, Err: cause})
	case l.lostUnsent.Load():
		send(Event{Kind: Lost, Link: l.id, Addr: l.addr})
	}
}

// linkDown is the connection status callback's reaction to a link that
// Windows reports down. It runs on a thread of the thread pool.
func (t *Transport) linkDown(l *link) {
	if !l.seenUp.Load() {
		return // the state before the link first came up
	}
	if l.downFlag.CompareAndSwap(false, true) {
		close(l.down)
	}
	if l.state.CompareAndSwap(stLive, stEnded) {
		// Queue the release before the consumer can see Lost, so that a
		// Connect it answers with is queued after it.
		t.post(command{kind: cmdRelease, link: l})
		if !t.trySend(Event{Kind: Lost, Link: l.id, Addr: l.addr}) {
			l.lostUnsent.Store(true)
		}
	}
}
