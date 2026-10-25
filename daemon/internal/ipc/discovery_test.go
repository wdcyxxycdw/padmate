package ipc

import (
	"strings"
	"testing"
)

func TestDiscoveryStrictValidation(t *testing.T) {
	d := Discovery{Pipe: `\\.\pipe\padmate-test`, PID: 123, Protocol: Protocol, Token: strings.Repeat("ab", 32)}
	data, err := encodeDiscovery(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeDiscovery(data)
	if err != nil || got != d {
		t.Fatalf("discovery roundtrip: %v", err)
	}
	for _, bad := range []string{
		`{}`, `{"pipe":"\\\\remote\\pipe\\padmate","pid":1,"protocol":1,"token":"x"}`,
		strings.Replace(string(data), `"pid":123`, `"pid":0`, 1),
		strings.Replace(string(data), `"protocol":1`, `"protocol":2`, 1),
		string(data) + `{}`, strings.Replace(string(data), `"token":`, `"extra":true,"token":`, 1),
	} {
		if _, err := decodeDiscovery([]byte(bad)); err == nil {
			t.Fatal("accepted invalid discovery")
		}
	}
}

func FuzzDiscovery(f *testing.F) {
	f.Add([]byte(`{"pipe":"\\\\.\\pipe\\padmate-test","pid":1,"protocol":1,"token":"` + strings.Repeat("ab", 32) + `"}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := decodeDiscovery(data)
		if err != nil {
			return
		}
		encoded, err := encodeDiscovery(d)
		if err != nil {
			t.Fatal(err)
		}
		again, err := decodeDiscovery(encoded)
		if err != nil || again != d {
			t.Fatal("roundtrip changed discovery")
		}
	})
}
