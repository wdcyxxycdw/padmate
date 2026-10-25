package tray

import (
	"strings"
	"testing"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestNotificationDataLayoutAndBounds(t *testing.T) {
	n := balloonData(42, Notification{Title: strings.Repeat("画😀", 40), Body: strings.Repeat("😀", 200)})
	if unsafe.Sizeof(n) != 976 || unsafe.Offsetof(n.InfoTitle) != 820 || unsafe.Offsetof(n.BalloonIcon) != 968 {
		t.Fatalf("NOTIFYICONDATA layout: size=%d title=%d icon=%d", unsafe.Sizeof(n), unsafe.Offsetof(n.InfoTitle), unsafe.Offsetof(n.BalloonIcon))
	}
	if n.Window != 42 || n.ID != 100 || n.Flags != 0x10 || n.Size != 976 {
		t.Fatalf("notification identity: %+v", n)
	}
	for _, s := range []string{windows.UTF16ToString(n.InfoTitle[:]), windows.UTF16ToString(n.Info[:])} {
		if strings.ContainsRune(s, '\ufffd') {
			t.Fatal("split surrogate")
		}
	}
	if len(utf16.Encode([]rune(windows.UTF16ToString(n.InfoTitle[:])))) > 63 || n.Info[255] != 0 {
		t.Fatal("unterminated or oversized notification")
	}
}
