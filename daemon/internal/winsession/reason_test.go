package winsession_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

func TestReasonString(t *testing.T) {
	const all = winsession.Lock | winsession.Disconnect | winsession.Desktop | winsession.Sleep | winsession.End
	if winsession.AllReasons != all || all != 0x1f {
		t.Fatalf("AllReasons = %#x, want %#x", uint8(winsession.AllReasons), uint8(all))
	}
	for _, tc := range []struct {
		r     winsession.Reason
		want  string
		names []string
	}{
		{0, "none", []string{}},
		{winsession.Lock, "lock", []string{"lock"}},
		{winsession.Disconnect, "disconnect", []string{"disconnect"}},
		{winsession.Desktop, "desktop", []string{"desktop"}},
		{winsession.Sleep, "sleep", []string{"sleep"}},
		{winsession.End, "end", []string{"end"}},
		{winsession.Desktop | winsession.Lock, "lock,desktop", []string{"lock", "desktop"}},
		{winsession.End | winsession.Sleep | winsession.Disconnect, "disconnect,sleep,end", []string{"disconnect", "sleep", "end"}},
		{winsession.AllReasons, "lock,disconnect,desktop,sleep,end", []string{"lock", "disconnect", "desktop", "sleep", "end"}},
		{0x20, "0x20", []string{}},
		{winsession.Lock | 0xc0, "lock,0xc0", []string{"lock"}},
	} {
		if got := tc.r.String(); got != tc.want {
			t.Errorf("Reason(%#x).String() = %q, want %q", uint8(tc.r), got, tc.want)
		}
		if got := tc.r.Names(); !slices.Equal(got, tc.names) {
			t.Errorf("Reason(%#x).Names() = %q, want %q", uint8(tc.r), got, tc.names)
		}
		if got := tc.r.LogValue(); got.Kind() != slog.KindString || got.String() != tc.want {
			t.Errorf("Reason(%#x).LogValue() = %v %q, want the string %q", uint8(tc.r), got.Kind(), got, tc.want)
		}
	}
}

func TestReasonMarshalText(t *testing.T) {
	b, err := json.Marshal(struct {
		R winsession.Reason `json:"r"`
		M map[string]winsession.Reason
	}{winsession.Lock | winsession.Sleep, map[string]winsession.Reason{"x": 0}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"r":"lock,sleep","M":{"x":"none"}}`; string(b) != want {
		t.Errorf("json.Marshal = %s, want %s", b, want)
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	log.Info("m", "reasons", winsession.Disconnect|winsession.Desktop, "was", winsession.Reason(0))
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line %q: %v", buf.String(), err)
	}
	if rec["reasons"] != "disconnect,desktop" || rec["was"] != "none" {
		t.Errorf("slog JSON = %s, want reasons disconnect,desktop and was none", strings.TrimSpace(buf.String()))
	}

	buf.Reset()
	slog.New(slog.NewTextHandler(&buf, nil)).Info("m", "reasons", winsession.End)
	if !strings.Contains(buf.String(), "reasons=end") {
		t.Errorf("slog text = %q, want reasons=end", buf.String())
	}
}
