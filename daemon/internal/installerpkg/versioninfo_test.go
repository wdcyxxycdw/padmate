package installerpkg

import (
	"bytes"
	"testing"
	"unicode/utf16"

	resversion "github.com/tc-hib/winres/version"
)

func utf16le(s string) []byte {
	var b []byte
	for _, c := range utf16.Encode([]rune(s)) {
		b = append(b, byte(c), byte(c>>8))
	}
	return b
}

func sampleVersionResource(t *testing.T) []byte {
	t.Helper()
	info := resversion.Info{FileVersion: [4]uint16{0, 14, 0, 7}, ProductVersion: [4]uint16{1, 2, 3, 4}}
	for key, value := range map[string]string{
		resversion.FileVersion:    "0.14.0.0",
		resversion.ProductVersion: "0.14.0-rc.1",
		resversion.ProductName:    "padmate 安装包",
	} {
		if err := info.Set(resversion.LangNeutral, key, value); err != nil {
			t.Fatal(err)
		}
	}
	return info.Bytes()
}

func TestParseVersionInfo(t *testing.T) {
	data := sampleVersionResource(t)
	check := func(name string, data []byte) {
		t.Helper()
		got, err := parseVersionInfo(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.File != [4]uint16{0, 14, 0, 7} || got.Product != [4]uint16{1, 2, 3, 4} {
			t.Errorf("%s: numeric versions %v %v", name, got.File, got.Product)
		}
		want := map[string]string{"FileVersion": "0.14.0.0", "ProductVersion": "0.14.0-rc.1", "ProductName": "padmate 安装包"}
		for k, v := range want {
			if got.Strings[k] != v {
				t.Errorf("%s: %s = %q, want %q", name, k, got.Strings[k], v)
			}
		}
	}
	check("neutral", data)
	// NSIS labels its tables with a Windows code page (1252) rather than
	// Unicode; the strings are UTF-16 all the same.
	nsis := bytes.Replace(data, utf16le("000004b0"), utf16le("040904e4"), 1)
	if bytes.Equal(nsis, data) {
		t.Fatal("the table key was not found to rewrite")
	}
	check("code page 1252", nsis)
}

func TestParseVersionInfoRejectsDamage(t *testing.T) {
	data := sampleVersionResource(t)
	for name, bad := range map[string][]byte{
		"empty":     nil,
		"short":     data[:5],
		"truncated": data[:len(data)/2],
		"wrong key": bytes.Replace(data, utf16le("VS_VERSION_INFO"), utf16le("VS_VERSION_INFX"), 1),
		"zeros":     make([]byte, 80),
	} {
		if got, err := parseVersionInfo(bad); err == nil {
			t.Errorf("%s: parsed as %+v", name, got)
		}
	}
}

func FuzzParseVersionInfo(f *testing.F) {
	info := resversion.Info{}
	_ = info.Set(resversion.LangNeutral, resversion.FileVersion, "1.0")
	f.Add(info.Bytes())
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = parseVersionInfo(data)
	})
}
