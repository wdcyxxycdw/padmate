package supervisor_test

import (
	"context"
	"testing"
)

func TestCaptureLeasesReleaseAndPreserveUserPause(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.set(10)
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	set := func(owner uint64, on bool) {
		t.Helper()
		if err := h.sup.Capture(ctx, owner, on); err != nil {
			t.Fatal(err)
		}
	}
	set(1, true)
	h.expect("+Space@0 -Space@10")
	if st := h.sup.Status(); !st.Capturing || st.Paused || st.Mapping() {
		t.Fatalf("capture status=%+v", st)
	}
	set(2, true)
	set(1, false)
	if !h.sup.Status().Capturing {
		t.Fatal("one client ended another client's capture")
	}
	h.pause(true)
	set(2, false)
	if st := h.sup.Status(); st.Capturing || !st.Paused {
		t.Fatalf("capture changed user pause: %+v", st)
	}
	h.pause(false)
	h.sync()
	h.expect("+Space@0 -Space@10")
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.set(20)
	h.expect("+Space@0 -Space@10 +Space@10")
	set(1, true)
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	set(1, false)
	h.sync()
	h.expect("+Space@0 -Space@10 +Space@10 -Space@20")
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.send(button(1, "east", false))
	h.sync()
	h.expect("+Space@0 -Space@10 +Space@10 -Space@20 +Space@20 -Space@20")
	if err := h.sup.Capture(ctx, 0, true); err == nil {
		t.Fatal("zero capture owner accepted")
	}
}
