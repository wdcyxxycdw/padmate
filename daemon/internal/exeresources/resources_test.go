package exeresources

import "testing"

func TestFixedVersionAndSafeLabels(t *testing.T) {
	for _, tc := range []struct {
		label string
		want  [4]uint16
		ok    bool
	}{
		{"v0.7.0-preview", [4]uint16{0, 7, 0, 0}, true},
		{"v1.2.3-12-gabcdef-dirty", [4]uint16{1, 2, 3, 0}, true},
		{"1.2.3.4", [4]uint16{1, 2, 3, 4}, true},
		{"dev", [4]uint16{}, true},
		{"0d89489", [4]uint16{}, true},
		{"v65536.2.3", [4]uint16{}, false},
		{"../escape", [4]uint16{}, false},
		{"bad\\path", [4]uint16{}, false},
		{"version -H=windowsgui", [4]uint16{}, false},
		{"", [4]uint16{}, false},
	} {
		got, err := FixedVersion(tc.label)
		if (err == nil) != tc.ok || (tc.ok && got != tc.want) {
			t.Errorf("FixedVersion(%q)=%v,%v; want %v ok=%v", tc.label, got, err, tc.want, tc.ok)
		}
	}
}

func FuzzFixedVersion(f *testing.F) {
	for _, seed := range []string{"v0.7.0-preview", "dev", "65536.65536.65536", "../file", "0d89489"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, label string) {
		fixed, err := FixedVersion(label)
		if err == nil {
			again, err := FixedVersion(label)
			if err != nil || fixed != again {
				t.Fatal("version parse was not deterministic")
			}
		}
	})
}
