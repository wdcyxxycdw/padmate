package ownwindow

import "testing"

func TestTrustedUIPaths(t *testing.T) {
	for _, tc := range []struct {
		daemon, ui, bridge string
		want               bool
	}{
		{`D:\padmate\bin\padmated.exe`, `D:\padmate\app\node_modules\electron\dist\electron.exe`, `D:\padmate\bin\padmatectl.exe`, true},
		{`D:\padmate\resources\bin\padmated.exe`, `D:\padmate\padmate.exe`, `D:\padmate\resources\bin\padmatectl.exe`, true},
		{`D:\padmate\resources\bin\padmated.exe`, `D:\foreign\padmate.exe`, `D:\padmate\resources\bin\padmatectl.exe`, false},
		{`D:\padmate\bin\padmated.exe`, `D:\padmate\app\node_modules\electron\dist\electron.exe`, `D:\foreign\padmatectl.exe`, false},
		{`D:\padmate\bin\padmated.exe`, `D:\foreign\electron.exe`, `D:\padmate\bin\padmatectl.exe`, false},
	} {
		if got := TrustedPaths(tc.daemon, tc.ui, tc.bridge); got != tc.want {
			t.Errorf("%+v got %v", tc, got)
		}
	}
}
