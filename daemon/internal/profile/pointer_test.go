package profile

import (
	"slices"
	"testing"
)

func TestPointer(t *testing.T) {
	for _, tc := range []struct {
		tokens []string
		want   string
	}{
		{nil, ""},
		{[]string{""}, "/"},
		{[]string{"layers", "base", "south"}, "/layers/base/south"},
		{[]string{"a/b", "m~n", "~1", "/~"}, "/a~1b/m~0n/~01/~1~0"},
		{[]string{"撤销", "0"}, "/撤销/0"},
	} {
		got := pointer(tc.tokens)
		if got != tc.want {
			t.Errorf("pointer(%q) = %q, want %q", tc.tokens, got, tc.want)
		}
		back, ok := splitPointer(got)
		if !ok || !slices.Equal(back, tc.tokens) && len(back)+len(tc.tokens) > 0 {
			t.Errorf("splitPointer(%q) = %q, %v; want %q", got, back, ok, tc.tokens)
		}
	}
	for _, bad := range []string{"layers", "/a~", "/a~2", "/~x"} {
		if _, ok := splitPointer(bad); ok {
			t.Errorf("splitPointer(%q) succeeded", bad)
		}
	}
}
