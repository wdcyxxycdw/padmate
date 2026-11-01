package main

import (
	"log/slog"
	"strconv"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// joyCon2Known converts the remembered Joy-Con 2 controllers of state.json
// (profile.State.JoyCon2) to the backend's list. An entry that cannot be read
// is skipped with a warning: the schema checks the shape, so only a hand-made
// file gets there. A nil memory (no library) remembers nothing. log may be nil.
func joyCon2Known(m *profilelib.Memory, log *slog.Logger) []input.KnownJoyCon2 {
	if m == nil {
		return nil
	}
	var out []input.KnownJoyCon2
	for _, c := range m.JoyCon2() {
		k, ok := knownJoyCon2(c)
		if !ok {
			if log != nil {
				log.Warn("remembered joy-con 2 skipped: unreadable entry in state.json", "serial", c.Serial)
			}
			continue
		}
		out = append(out, k)
	}
	return out
}

// knownJoyCon2 converts one entry of state.json.
func knownJoyCon2(c profile.JoyCon2Controller) (input.KnownJoyCon2, bool) {
	addr, ok := parseJoyCon2Addr(c.Addr)
	if !ok || c.Serial == "" {
		return input.KnownJoyCon2{}, false
	}
	var side jc2.Side
	switch c.Side {
	case "L":
		side = jc2.SideL
	case "R":
		side = jc2.SideR
	default:
		return input.KnownJoyCon2{}, false
	}
	return input.KnownJoyCon2{Addr: addr, Serial: c.Serial, Side: side}, true
}

// parseJoyCon2Addr reads AA:BB:CC:DD:EE:FF, the form KnownJoyCon2.AddrText
// writes.
func parseJoyCon2Addr(text string) (uint64, bool) {
	parts := strings.Split(text, ":")
	if len(parts) != 6 {
		return 0, false
	}
	var addr uint64
	for _, p := range parts {
		if len(p) != 2 {
			return 0, false
		}
		b, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			return 0, false
		}
		addr = addr<<8 | b
	}
	return addr, addr != 0
}
