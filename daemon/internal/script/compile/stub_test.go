package compile_test

import (
	"regexp"
	"slices"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
)

// The virtual padmate module exports every value the current API declares,
// and nothing else: a member the host has but the module lacks cannot be
// imported (esbuild: "No matching export"), as hud could not at first.
func TestPadmateStubExportsTheDeclaredValues(t *testing.T) {
	want, err := dtsgen.ValueMembers(dtsgen.HostAPI)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range regexp.MustCompile(`(?m)^export const (\w+) = bridge\.(\w+);$`).FindAllStringSubmatch(compile.PadmateStub, -1) {
		if m[1] != m[2] {
			t.Errorf("export %s is bridge.%s", m[1], m[2])
		}
		got = append(got, m[1])
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("padmate module exports %q; API %s declares %q", got, dtsgen.HostAPI, want)
	}
}
