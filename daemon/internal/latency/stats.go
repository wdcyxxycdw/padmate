package latency

import (
	"sync"
	"time"
)

// The product statistics of padmated (DMN-15, ADR-0071).
//
// A Recorder keeps the latest RingSize latencies of each device: L is the time
// from T-input receiving the event of an input edge (input.Event.Stamp) to the
// return of the Send call that carries its keys (output.InputMark). It is
// measured in the daemon on every real key, unlike the bench of ENG-11, whose
// L starts at SDL's own timestamp and which only the tests run (ARCHITECTURE
// §13).
const (
	// RingSize is how many of the latest samples a device keeps.
	RingSize = 1000
	// MaxDevices is how many devices a Recorder keeps samples of. The slots
	// map at most four devices at once; the rest is room for devices that
	// came and went, whose samples the least recently written ring gives up.
	MaxDevices = 8
	// WarnP99 is the product threshold of the statistics: a p99 above it is
	// shown as a warning. It is also the target of the bench (ENG-11,
	// ARCHITECTURE §13), whose plan takes it from here.
	WarnP99 = 1500 * time.Microsecond
	// WarnMinSamples is how many samples a device needs before its p99 can
	// warn: a p99 of a handful of samples is its maximum.
	WarnMinSamples = 50
)

// Recorder keeps the latest RingSize latencies of up to MaxDevices devices,
// each under a key, the DeviceKey of the device. Record is for T-output: it
// allocates nothing and never waits for anything but a mutex that the
// readers hold for the copy of a ring. It is safe for concurrent use.
//
// The rings are one array inside the Recorder, so that a new device takes a
// ring without allocating: a free one, or the least recently written one when
// all MaxDevices are in use.
type Recorder struct {
	mu    sync.Mutex
	seq   uint64
	rings [MaxDevices]ring
}

// ring is the latest RingSize samples of one device.
type ring struct {
	key     string
	used    bool
	touched uint64 // the Recorder's seq at the last write, for eviction
	n       int    // valid samples, at most RingSize
	next    int    // where the next sample goes
	buf     [RingSize]time.Duration
}

// NewRecorder returns an empty Recorder.
func NewRecorder() *Recorder { return new(Recorder) }

// find returns the ring of key, or nil. The caller holds mu.
func (r *Recorder) find(key string) *ring {
	for i := range r.rings {
		if g := &r.rings[i]; g.used && g.key == key {
			return g
		}
	}
	return nil
}

// Record adds the latency d of the device key. When the device has no ring and
// all MaxDevices are in use, it takes the ring written least recently, and its
// samples are gone. It does not allocate.
func (r *Recorder) Record(key string, d time.Duration) {
	r.mu.Lock()
	r.seq++
	g := r.find(key)
	if g == nil {
		g = &r.rings[0]
		for i := range r.rings {
			c := &r.rings[i]
			if !c.used {
				g = c
				break
			}
			if c.touched < g.touched {
				g = c
			}
		}
		g.key, g.used, g.n, g.next = key, true, 0, 0
	}
	g.touched = r.seq
	g.buf[g.next] = d
	g.next = (g.next + 1) % RingSize
	g.n = min(g.n+1, RingSize)
	r.mu.Unlock()
}

// Summary returns the distribution of the samples of the device key: the zero
// Summary for a device without samples. It is Summarize of a copy, so it
// allocates (the clone that Summarize sorts); it runs on the goroutine of the
// reader, not on T-output. The copy under the mutex goes to the stack, so that
// nothing is allocated while Record of T-output may wait for the mutex.
func (r *Recorder) Summary(key string) Summary {
	var buf [RingSize]time.Duration
	n := 0
	r.mu.Lock()
	if g := r.find(key); g != nil {
		n = copy(buf[:], g.buf[:g.n])
	}
	r.mu.Unlock()
	return Summarize(buf[:n])
}

// Reset forgets the samples of the device key.
func (r *Recorder) Reset(key string) {
	r.mu.Lock()
	if g := r.find(key); g != nil {
		g.used, g.key, g.n, g.next = false, "", 0, 0
	}
	r.mu.Unlock()
}

// ResetAll forgets the samples of every device.
func (r *Recorder) ResetAll() {
	r.mu.Lock()
	for i := range r.rings {
		g := &r.rings[i]
		g.used, g.key, g.n, g.next = false, "", 0, 0
	}
	r.mu.Unlock()
}

// Warn reports whether s is bad enough to warn about: it has WarnMinSamples
// samples and its p99 is above WarnP99.
func Warn(s Summary) bool { return s.N >= WarnMinSamples && s.P99 > WarnP99 }
