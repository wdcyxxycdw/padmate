package tray

import (
	"fmt"
	"sync"
)

// The tray's Joy-Con 2 item (「连接新的 Joy-Con 2」, a literal in
// cmd/padmated/tray_windows.go like the other items) and its notices. The
// item is shown only when the
// daemon has Joy-Con 2 support (Presentation.JoyCon2); a click opens the
// pairing window, and the notices say what became of it. The text is the
// owner's.
const (
	// JoyCon2WindowSeconds is how long the tray's pairing window stays open.
	JoyCon2WindowSeconds = 30

	joyCon2NoticeTitle = "Joy-Con 2"
)

// JoyCon2Pairing follows the pairing window that a click on the tray item
// opened and words its notices. A window that something else opened (the
// window of the UI, padmatectl) gets none: the one who asked shows the result.
// All its methods are safe for concurrent use.
type JoyCon2Pairing struct {
	mu     sync.Mutex
	active bool
	seq    uint64
}

// Active reports whether a window of the tray is open.
func (p *JoyCon2Pairing) Active() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active
}

// Start is called when the item is clicked, before the window is opened. It
// returns the notice that tells the user what to do.
func (p *JoyCon2Pairing) Start() Notification {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active = true
	p.seq++
	return p.notice("start", fmt.Sprintf("请在 %d 秒内按住 Joy-Con 2 侧面的 SYNC 键。", JoyCon2WindowSeconds))
}

// Cancel forgets the window without a notice: it could not be opened, or
// somebody closed or reopened it.
func (p *JoyCon2Pairing) Cancel() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active = false
}

// Remembered is called when a controller was connected and remembered, side
// "L" or "R". It returns the notice if the window was the tray's.
func (p *JoyCon2Pairing) Remembered(side string) (Notification, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active {
		return Notification{}, false
	}
	p.active = false
	text := "已连接并记住 Joy-Con 2。"
	switch side {
	case "L":
		text = "已连接并记住 Joy-Con 2（左）。"
	case "R":
		text = "已连接并记住 Joy-Con 2（右）。"
	}
	return p.notice("found", text), true
}

// Closed is called when the window ended without a controller. It returns the
// notice if the window was the tray's.
func (p *JoyCon2Pairing) Closed() (Notification, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active {
		return Notification{}, false
	}
	p.active = false
	return p.notice("none", fmt.Sprintf("%d 秒内没有找到 Joy-Con 2。", JoyCon2WindowSeconds)), true
}

// notice builds a notice with a key of its own for this window, since the
// queue never shows a key twice. The caller holds p.mu.
func (p *JoyCon2Pairing) notice(kind, body string) Notification {
	return Notification{Key: fmt.Sprintf("joycon2:%d:%s", p.seq, kind), Title: joyCon2NoticeTitle, Body: body}
}
