package mapwizard

import (
	"errors"
	"strings"
	"testing"
)

const testGUID = "ff00aabb000000000000000000007600"

// run drives a Session: it keeps the time and gives the elements the
// wizard is pressed with.
type run struct {
	t   *testing.T
	s   *Session
	now int64
}

func newRun(t *testing.T, info Info) *run {
	t.Helper()
	r := &run{t: t, s: NewSession(strings.ToUpper(testGUID), info, 0)}
	// Quiet: nothing happens, then the first prompt begins.
	r.now = Quiet
	r.s.Tick(r.now)
	return r
}

func (r *run) feed(k Kind, index, value int) Result {
	r.now += 5_000_000
	return r.s.Feed(Event{Kind: k, Index: index, Value: value, Time: r.now})
}

// press presses and releases button b for the current step.
func (r *run) press(b int) Result {
	r.feed(Button, b, 1)
	return r.feed(Button, b, 0)
}

// push moves axis a to v and lets it go back to rest.
func (r *run) push(a, v, rest int) Result {
	r.feed(Axis, a, v)
	return r.feed(Axis, a, rest)
}

func (r *run) mustBind(res Result, step string) {
	r.t.Helper()
	if res.Bound != step {
		cur, st, _ := r.s.Current()
		r.t.Fatalf("bound %q (rejected %q), want %q; now at step %d %q", res.Bound, res.Rejected, step, cur, st.ID)
	}
}

func (r *run) mapping() string {
	r.t.Helper()
	m, _, err := r.s.Mapping()
	if err != nil {
		r.t.Fatal(err)
	}
	return m.String()
}

func TestQuietPeriodBindsNothing(t *testing.T) {
	s := NewSession(testGUID, Info{Axes: 2, Buttons: 8}, 100)
	if !s.Quiet(100 + Quiet - 1) {
		t.Error("the quiet period ends early")
	}
	// A press and release in the quiet period are not an answer.
	if r := s.Feed(Event{Kind: Button, Index: 1, Value: 1, Time: 200}); r.Bound != "" {
		t.Errorf("bound during the quiet period: %+v", r)
	}
	if r := s.Feed(Event{Kind: Button, Index: 1, Value: 0, Time: 300}); r.Bound != "" {
		t.Errorf("bound during the quiet period: %+v", r)
	}
	if i, st, ok := s.Current(); !ok || i != 0 || st.ID != "a" {
		t.Errorf("Current = %d %+v %v", i, st, ok)
	}
	// The first event after the quiet period begins the prompt.
	if r := s.Feed(Event{Kind: Button, Index: 2, Value: 1, Time: 100 + Quiet + 1}); r.Bound != "" {
		t.Errorf("a press alone bound %+v", r)
	}
	if r := s.Feed(Event{Kind: Button, Index: 2, Value: 0, Time: 100 + Quiet + 2}); r.Bound != "a" {
		t.Errorf("release bound %+v, want a", r)
	}
}

func TestSessionBuildsAMapping(t *testing.T) {
	r := newRun(t, Info{Axes: 6, Buttons: 14, Hats: 1, AxisRest: []int16{0, 0, 0, 0, -32768, -32768}})
	r.mustBind(r.press(0), "a")
	r.mustBind(r.press(1), "b")
	r.mustBind(r.press(2), "x")
	r.mustBind(r.press(3), "y")
	for _, mask := range []int{1, 4, 8, 2} { // dpup, dpdown, dpleft, dpright
		r.feed(Hat, 0, mask)
		res := r.feed(Hat, 0, 0)
		_ = res
	}
	// The hats counted when pushed, not when released: look where we are.
	if i, st, _ := r.s.Current(); st.ID != "ls-left" {
		t.Fatalf("after the d-pad at step %d %q, want ls-left", i, st.ID)
	}
	r.mustBind(r.push(0, -30000, 0), "ls-left")
	r.mustBind(r.push(0, 30000, 0), "ls-right")
	r.mustBind(r.push(1, -30000, 0), "ls-up")
	r.mustBind(r.push(1, 30000, 0), "ls-down")
	r.mustBind(r.press(8), "ls-click")
	r.mustBind(r.push(2, -30000, 0), "rs-left")
	r.mustBind(r.push(2, 30000, 0), "rs-right")
	r.mustBind(r.push(3, 30000, 0), "rs-up") // this stick's y runs backwards
	r.mustBind(r.push(3, -30000, 0), "rs-down")
	r.mustBind(r.press(9), "rs-click")
	r.mustBind(r.press(4), "lb")
	r.mustBind(r.press(5), "rb")
	r.mustBind(r.push(4, 32767, -32768), "lt")
	r.mustBind(r.push(5, 32767, -32768), "rt")
	r.mustBind(r.press(6), "view")
	r.mustBind(r.press(7), "menu")
	r.mustBind(r.press(10), "home")
	if !r.s.Done() {
		t.Error("the session is not done after the last step")
	}
	want := testGUID + ",*,a:b0,b:b1,x:b2,y:b3,dpup:h0.1,dpdown:h0.4,dpleft:h0.8,dpright:h0.2," +
		"leftx:a0,lefty:a1,leftstick:b8,rightx:a2,righty:a3~,rightstick:b9," +
		"leftshoulder:b4,rightshoulder:b5,lefttrigger:a4,righttrigger:a5,back:b6,start:b7,guide:b10,platform:Windows"
	m, warns, err := r.s.Mapping()
	if err != nil {
		t.Fatal(err)
	}
	if m.String() != want {
		t.Errorf("Mapping =\n%s\nwant\n%s", m, want)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %+v, want none", warns)
	}
	if err := m.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

// skipTo skips steps until the current one is id.
func (r *run) skipTo(id string) {
	r.t.Helper()
	for {
		_, st, ok := r.s.Current()
		if !ok {
			r.t.Fatalf("no step %q", id)
		}
		if st.ID == id {
			return
		}
		r.s.Skip()
	}
}

func TestStickDirectionsMerge(t *testing.T) {
	info := Info{Axes: 4, Buttons: 12}
	tests := []struct {
		name string
		do   func(r *run)
		want string
	}{
		{"left stick the natural way", func(r *run) {
			r.skipTo("ls-left")
			r.push(0, -30000, 0)
			r.push(0, 30000, 0)
		}, "leftx:a0"},
		{"left stick inverted", func(r *run) {
			r.skipTo("ls-left")
			r.push(0, 30000, 0)
			r.push(0, -30000, 0)
		}, "leftx:a0~"},
		{"left and right on buttons", func(r *run) {
			r.skipTo("ls-left")
			r.press(8)
			r.press(9)
		}, "-leftx:b8,+leftx:b9"},
		{"left and right on two axes", func(r *run) {
			r.skipTo("ls-left")
			r.push(0, -30000, 0)
			r.push(2, 30000, 0)
		}, "-leftx:-a0,+leftx:+a2"},
		{"only one direction", func(r *run) {
			r.skipTo("ls-left")
			r.push(0, -30000, 0)
		}, "-leftx:-a0"},
		{"only the other direction, on a button", func(r *run) {
			r.skipTo("ls-right")
			r.press(5)
		}, "+leftx:b5"},
		{"vertical axis, up is negative", func(r *run) {
			r.skipTo("ls-up")
			r.push(1, -30000, 0)
			r.push(1, 30000, 0)
		}, "lefty:a1"},
		{"the same direction twice is a duplicate, not a pair", func(r *run) {
			r.skipTo("ls-left")
			r.push(0, -30000, 0)
			r.push(0, -30000, 0)
		}, "+leftx:-a0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRun(t, info)
			tc.do(r)
			got := r.mapping()
			if want := testGUID + ",*," + tc.want + ",platform:Windows"; got != want {
				t.Errorf("Mapping = %s, want %s", got, want)
			}
		})
	}
}

func TestDuplicateElementClearsTheEarlierStep(t *testing.T) {
	r := newRun(t, Info{Buttons: 8})
	r.mustBind(r.press(3), "a")
	res := r.press(3)
	r.mustBind(res, "b")
	if len(res.Cleared) != 1 || res.Cleared[0] != "a" {
		t.Errorf("Cleared = %v, want [a]", res.Cleared)
	}
	if _, ok := r.s.Binding("a"); ok {
		t.Error("a is still bound after b took its button")
	}
	if in, ok := r.s.Binding("b"); !ok || in.Kind != Button || in.Index != 3 {
		t.Errorf("b = %+v %v", in, ok)
	}
}

func TestSkipBackRedo(t *testing.T) {
	r := newRun(t, Info{Buttons: 8})
	r.mustBind(r.press(0), "a")
	r.s.Skip() // b
	if _, st, _ := r.s.Current(); st.ID != "x" {
		t.Fatalf("after a skip at %q, want x", st.ID)
	}
	r.mustBind(r.press(2), "x")
	// Back asks y... no: back goes to the step before the current one (y)
	// and throws its answer away. Here y is current; go back to x.
	r.s.Back()
	if _, st, _ := r.s.Current(); st.ID != "x" {
		t.Fatalf("after Back at %q, want x", st.ID)
	}
	if _, ok := r.s.Binding("x"); ok {
		t.Error("x kept its answer after Back")
	}
	r.mustBind(r.press(5), "x")
	// Redo drops a half-done prompt: a press that never released.
	r.feed(Button, 6, 1)
	r.s.Redo()
	if res := r.feed(Button, 6, 0); res.Bound != "" {
		t.Errorf("a release after Redo bound %+v; the press was forgotten", res)
	}
	r.mustBind(r.press(6), "y")
	// Back at the first step stays there.
	first := NewSession(testGUID, Info{}, 0)
	first.Back()
	if i, _, _ := first.Current(); i != 0 {
		t.Errorf("Back at the first step moved to %d", i)
	}
	// Mapping: a, x and y are bound; b is not.
	if got := r.mapping(); got != testGUID+",*,a:b0,x:b5,y:b6,platform:Windows" {
		t.Errorf("Mapping = %s", got)
	}
}

func TestRedoAfterTheLastStep(t *testing.T) {
	r := newRun(t, Info{Buttons: 4})
	for range DefaultSteps {
		r.s.Skip()
	}
	if !r.s.Done() {
		t.Fatal("not done after skipping every step")
	}
	r.s.Redo()
	if _, st, ok := r.s.Current(); !ok || st.ID != "home" {
		t.Errorf("Redo after the end asks %q %v, want home", st.ID, ok)
	}
}

func TestMappingWarnsAboutViewAndMenu(t *testing.T) {
	r := newRun(t, Info{Buttons: 8})
	r.mustBind(r.press(0), "a")
	_, warns, err := r.s.Mapping()
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 2 || warns[0].Code != WarnNoBack || warns[1].Code != WarnNoStart {
		t.Errorf("warnings = %+v, want noBack and noStart", warns)
	}
	r.skipTo("view")
	r.press(6)
	_, warns, _ = r.s.Mapping()
	if len(warns) != 1 || warns[0].Code != WarnNoStart {
		t.Errorf("warnings = %+v, want noStart only", warns)
	}
}

func TestMappingOfNothingIsAnError(t *testing.T) {
	r := newRun(t, Info{})
	if _, _, err := r.s.Mapping(); !errors.Is(err, ErrNothingBound) {
		t.Errorf("Mapping() err = %v, want ErrNothingBound", err)
	}
}

func TestHatDiagonalIsReportedAndRetried(t *testing.T) {
	r := newRun(t, Info{Hats: 1, Buttons: 4})
	r.skipTo("dpup")
	if res := r.feed(Hat, 0, 3); res.Rejected != ReasonDiagonal || res.Bound != "" {
		t.Errorf("diagonal: %+v", res)
	}
	r.feed(Hat, 0, 0)
	r.mustBind(r.feed(Hat, 0, 1), "dpup")
}

func TestNoisyAxesAreReported(t *testing.T) {
	s := NewSession(testGUID, Info{Axes: 3}, 0)
	for i := range 6 {
		v := 20000
		if i%2 == 1 {
			v = -20000
		}
		s.Feed(Event{Kind: Axis, Index: 2, Value: v, Time: int64(i) * 1_000_000})
	}
	if got := s.Noisy(); len(got) != 1 || got[0] != 2 {
		t.Errorf("Noisy = %v, want [2]", got)
	}
}

// TestSessionMapsAStickThatAThumbRestedOn: the pad is held with a thumb on
// the left stick while the dialog opens, and the stick sends a few events in
// the quiet period. It is left out then, but the user is not locked out of
// mapping it: once the stick has been still for a second it counts.
func TestSessionMapsAStickThatAThumbRestedOn(t *testing.T) {
	s := NewSession(testGUID, Info{Axes: 2, Buttons: 8}, 0)
	for i, v := range []int{600, 900, 1200, 800, 500, 300} {
		s.Feed(Event{Kind: Axis, Index: 0, Value: v, Time: int64(i+1) * 100_000_000})
	}
	if got := s.Noisy(); len(got) != 1 || got[0] != 0 {
		t.Fatalf("Noisy() = %v, want the left stick's axis", got)
	}
	for range 8 { // a b x y and the d-pad
		s.Skip()
	}
	if _, st, _ := s.Current(); st.ID != "ls-left" {
		t.Fatalf("at step %q, want ls-left", st.ID)
	}
	at := int64(3 * 1_000_000_000)
	s.Feed(Event{Kind: Axis, Index: 0, Value: -30000, Time: at})
	res := s.Feed(Event{Kind: Axis, Index: 0, Value: 0, Time: at + 10_000_000})
	if res.Bound != "ls-left" || res.Input.Key() != "a0-" {
		t.Errorf("the stick pushed left after a second of rest gave %+v, want ls-left bound to a0-", res)
	}
	if got := s.Noisy(); len(got) != 0 {
		t.Errorf("Noisy() = %v after the stick has been still", got)
	}
}
