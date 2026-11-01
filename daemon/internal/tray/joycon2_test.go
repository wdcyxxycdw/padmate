package tray

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

func TestJoyCon2PairingNotices(t *testing.T) {
	const title = "Joy-Con 2"
	tests := []struct {
		name string
		do   func(p *JoyCon2Pairing) (Notification, bool)
		body string
	}{
		{"left remembered", func(p *JoyCon2Pairing) (Notification, bool) { p.Start(); return p.Remembered("L") }, "已连接并记住 Joy-Con 2（左）。"},
		{"right remembered", func(p *JoyCon2Pairing) (Notification, bool) { p.Start(); return p.Remembered("R") }, "已连接并记住 Joy-Con 2（右）。"},
		{"remembered without a side", func(p *JoyCon2Pairing) (Notification, bool) { p.Start(); return p.Remembered("") }, "已连接并记住 Joy-Con 2。"},
		{"nothing found", func(p *JoyCon2Pairing) (Notification, bool) { p.Start(); return p.Closed() }, "30 秒内没有找到 Joy-Con 2。"},
		{"remembered in a window of another client", func(p *JoyCon2Pairing) (Notification, bool) { return p.Remembered("L") }, ""},
		{"closed in a window of another client", func(p *JoyCon2Pairing) (Notification, bool) { return p.Closed() }, ""},
		{"canceled, then closed", func(p *JoyCon2Pairing) (Notification, bool) { p.Start(); p.Cancel(); return p.Closed() }, ""},
		{"answered once", func(p *JoyCon2Pairing) (Notification, bool) {
			p.Start()
			p.Remembered("L")
			return p.Closed() // the window closes right after the controller connected
		}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n, ok := tc.do(new(JoyCon2Pairing))
			if ok != (tc.body != "") || n.Body != tc.body {
				t.Fatalf("got %q, %v; want %q", n.Body, ok, tc.body)
			}
			if ok && (n.Title != title || n.Key == "" || n.OncePerRun) {
				t.Fatalf("notice %+v", n)
			}
		})
	}

	var p JoyCon2Pairing
	start := p.Start()
	if start.Body != "请在 30 秒内按住 Joy-Con 2 侧面的 SYNC 键。" || start.Title != title || !p.Active() {
		t.Fatalf("start: %+v active=%v", start, p.Active())
	}
	p.Cancel()
	if p.Active() {
		t.Fatal("still active after Cancel")
	}

	// Every window has keys of its own, or the queue would drop the notice of
	// the second one.
	q := NewNotifications(16)
	var w JoyCon2Pairing
	for range 3 {
		for i := range 2 {
			var n Notification
			if i == 0 {
				n = w.Start()
			} else {
				n, _ = w.Closed()
			}
			if !q.Add(n) {
				t.Fatalf("notice %+v dropped", n)
			}
		}
	}
	var again JoyCon2Pairing
	a, b := again.Start(), again.Start()
	if a.Key == b.Key {
		t.Fatal("two windows of one tray share a key")
	}
}

func TestJoyCon2ItemHidden(t *testing.T) {
	// Present never shows the item by itself.
	if Present(supervisor.Status{}, nil).JoyCon2 {
		t.Fatal("Present shows the Joy-Con 2 item")
	}
}
