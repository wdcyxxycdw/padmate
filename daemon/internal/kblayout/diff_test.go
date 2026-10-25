package kblayout

import (
	"encoding/json"
	"fmt"
	"testing"
)

// fixtureDiff lists the differences between two fixtures of one layout,
// leaving out Generator, Version and Windows.
func fixtureDiff(want, got *Fixture) []string {
	var diffs []string
	for _, f := range []struct{ name, want, got string }{
		{"klid", want.KLID, got.KLID},
		{"hkl", want.HKL, got.HKL},
		{"layoutFile", want.LayoutFile, got.LayoutFile},
		{"layoutText", want.LayoutText, got.LayoutText},
		{"layerDriver", want.LayerDriver, got.LayerDriver},
	} {
		if f.want != f.got {
			diffs = append(diffs, fmt.Sprintf("%s: %q, want %q", f.name, f.got, f.want))
		}
	}
	diffs = append(diffs, entryDiff("scancodes", want.Scancodes, got.Scancodes, func(e ScanEntry) string {
		return fmt.Sprintf("sc=%v ext=%v", e.Scan, e.Ext)
	})...)
	diffs = append(diffs, entryDiff("vkToScan", want.VKToScan, got.VKToScan, func(e VKEntry) string {
		return "vk=" + e.VK.String()
	})...)
	diffs = append(diffs, entryDiff("chars", want.Chars, got.Chars, func(e CharEntry) string {
		return fmt.Sprintf("%q", e.Char)
	})...)
	return diffs
}

// entryDiff compares two lists of entries by key and lists the entries
// that are missing, extra or different, in JSON.
func entryDiff[E any](name string, want, got []E, key func(E) string) []string {
	enc := func(e E) string {
		b, err := json.Marshal(e)
		if err != nil {
			return err.Error()
		}
		return string(b)
	}
	wantBy := map[string]string{}
	var keys []string
	for _, e := range want {
		wantBy[key(e)] = enc(e)
		keys = append(keys, key(e))
	}
	gotBy := map[string]string{}
	for _, e := range got {
		k := key(e)
		gotBy[k] = enc(e)
		if _, ok := wantBy[k]; !ok {
			keys = append(keys, k)
		}
	}
	var diffs []string
	for _, k := range keys {
		w, inWant := wantBy[k]
		g, inGot := gotBy[k]
		switch {
		case !inGot:
			diffs = append(diffs, fmt.Sprintf("%s %s: missing, want %s", name, k, w))
		case !inWant:
			diffs = append(diffs, fmt.Sprintf("%s %s: extra %s", name, k, g))
		case w != g:
			diffs = append(diffs, fmt.Sprintf("%s %s: %s, want %s", name, k, g, w))
		}
	}
	return diffs
}

func TestFixtureDiff(t *testing.T) {
	want := loadFixture(t, klidUS)
	if diffs := fixtureDiff(want, loadFixture(t, klidUS)); len(diffs) != 0 {
		t.Errorf("a fixture differs from itself: %q", diffs)
	}
	got := loadFixture(t, klidUS)
	got.Generator, got.Version, got.Windows = "other", "v9", "11.0.1"
	got.LayoutText = "Other"
	got.Scancodes[0].VK = 0xFE
	got.VKToScan = got.VKToScan[1:]
	got.Chars[0].Shift = !got.Chars[0].Shift
	diffs := fixtureDiff(want, got)
	wantPrefixes := []string{
		`layoutText: "Other", want "US"`,
		"scancodes sc=0x01 ext=false: {",
		"vkToScan vk=" + want.VKToScan[0].VK.String() + ": missing, want {",
		`chars " ": {`,
	}
	if len(diffs) != len(wantPrefixes) {
		t.Fatalf("fixtureDiff = %q, want %d differences", diffs, len(wantPrefixes))
	}
	for i, p := range wantPrefixes {
		if len(diffs[i]) < len(p) || diffs[i][:len(p)] != p {
			t.Errorf("difference %d = %q, want it to start with %q", i, diffs[i], p)
		}
	}
}
