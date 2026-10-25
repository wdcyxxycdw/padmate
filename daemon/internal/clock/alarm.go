package clock

// Alarm is one Timer that follows an absolute deadline, for a loop that
// re-arms it after every wake. Set with the deadline it is armed for does
// nothing, so a wake that leaves the deadline as it was reads no time: a
// test that moves a Fake while the loop re-arms cannot push the deadline
// back. An Alarm is not safe for concurrent use.
type Alarm struct {
	clk   Clock
	t     Timer // nil until the first arming
	at    Time
	armed bool
}

// NewAlarm returns an Alarm on clk, not armed.
func NewAlarm(clk Clock) *Alarm { return &Alarm{clk: clk} }

// Set arms the alarm for at when ok, and stops it otherwise, so that it
// takes a NextDeadline result as is. A deadline not after Now fires at
// once.
func (a *Alarm) Set(at Time, ok bool) {
	switch {
	case !ok:
		a.Stop()
		return
	case a.armed && a.at == at:
		return
	}
	d := at.Sub(a.clk.Now())
	if a.t == nil {
		a.t = a.clk.NewTimer(d)
	} else {
		a.t.Reset(d)
	}
	a.at, a.armed = at, true
}

// C returns the channel the alarm fires on, or nil when it is not armed,
// which blocks in a select.
func (a *Alarm) C() <-chan Time {
	if !a.armed {
		return nil
	}
	return a.t.C()
}

// Fired tells the alarm that its value was received from C: it is no
// longer armed.
func (a *Alarm) Fired() { a.armed = false }

// Stop disarms the alarm.
func (a *Alarm) Stop() {
	if a.armed {
		a.t.Stop()
		a.armed = false
	}
}
