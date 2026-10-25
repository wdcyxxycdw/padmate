// Package tray presents immutable mapping snapshots to a desktop tray.
package tray

import (
	"fmt"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

type Presentation struct {
	Tooltip  string
	Details  []string
	Badge    int
	Inactive bool
}

// Present is PresentWithPower for pads whose power is not known.
func Present(st supervisor.Status, views []slot.View) Presentation {
	return PresentWithPower(st, views, nil)
}

// PowerParts words the power of a pad for the menu: "电量 80%" when it reports
// a charge, and "充电中" when it charges. It returns nothing for a pad whose
// power is unknown, and for one that is wired (plugged in, or behind a
// receiver that always says full): only a pad that discharges or charges shows
// a battery (ADR-0067).
func PowerParts(p input.Power) []string {
	var parts []string
	if (p.State == input.PowerDischarging || p.State == input.PowerCharging) && p.Percent >= 0 {
		parts = append(parts, fmt.Sprintf("电量 %d%%", p.Percent))
	}
	if p.State == input.PowerCharging {
		parts = append(parts, "充电中")
	}
	return parts
}

// PresentWithPower keeps a full line per mapped pad for the menu: the pad's name
// and its profile, and its power (IN-12) when the daemon has read one, from
// power by the pad's DeviceKey; nothing of layers or latched controls (DMN-18,
// the owner's G10), so the tooltip shows no control IDs or layer names. The
// tooltip does not have the power either: Windows limits the hover tooltip to
// 127 UTF-16 code units, including separators but excluding NUL, which the name
// and the profile fill. The icon's badge is the first mapped pad's mode, the
// numbering layer feedback uses; modes outside 1..4 have no badge.
func PresentWithPower(st supervisor.Status, views []slot.View, power map[string]input.Power) Presentation {
	p := Presentation{Inactive: !st.Mapping()}
	state := "运行中"
	if st.Paused {
		state = "已暂停"
	}
	if st.Capturing {
		if st.Paused {
			state += " · 捕获中"
		} else {
			state = "捕获中"
		}
	}
	if st.Suspended != 0 {
		state += " · 系统暂停"
	}
	var lines []string // the pads' lines without the power: the tooltip's
	for _, v := range views {
		if v.Device == 0 {
			continue
		}
		m := v.Mapping
		if len(p.Details) == 0 && m.Mode >= 1 && m.Mode <= 4 {
			p.Badge = m.Mode
		}
		pad := strings.TrimSpace(clean(v.Info.Name))
		if pad == "" {
			pad = fmt.Sprintf("手柄 %d", v.Index+1)
		}
		line := pad + " · " + clean(m.Profile)
		lines = append(lines, line)
		for _, part := range PowerParts(power[v.Key]) {
			line += " · " + part
		}
		p.Details = append(p.Details, line)
	}
	if len(p.Details) == 0 {
		line := "未连接手柄"
		if len(views) > 0 {
			line += " · " + clean(views[0].Mapping.Profile)
		}
		p.Details = append(p.Details, line)
		lines = p.Details
	}
	p.Tooltip = truncateUTF16("padmate · "+state+"\n"+strings.Join(lines, "\n"), 127)
	return p
}

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < ' ' {
			return ' '
		}
		return r
	}, s)
}

func truncateUTF16(s string, limit int) string {
	units, end := 0, 0
	for offset, r := range s {
		n := 1
		if r > 0xffff {
			n = 2
		}
		if units+n > limit {
			return s[:end] + "…"
		}
		units += n
		if units <= limit-1 {
			end = offset + len(string(r))
		}
	}
	return s
}
