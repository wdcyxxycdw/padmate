package output_test

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

func TestOwner(t *testing.T) {
	tests := []struct {
		o     output.Owner
		want  string
		valid bool
	}{
		{output.Owner{}, "0/0/invalid/0", false},
		{output.Owner{Slot: 0, Gen: 3, Source: output.SourceDecl, Cycle: 17}, "0/3/decl/17", true},
		{output.Owner{Slot: 2, Gen: 1, Source: output.SourceScript}, "2/1/script/0", true},
		{output.Owner{Slot: 255, Gen: 4294967295, Source: output.SourceDecl, Cycle: 18446744073709551615}, "255/4294967295/decl/18446744073709551615", true},
		{output.Owner{Slot: 1, Gen: 1, Source: 7, Cycle: 1}, "1/1/invalid/1", false},
	}
	for _, tt := range tests {
		if got := tt.o.String(); got != tt.want {
			t.Errorf("%#v.String() = %q, want %q", tt.o, got, tt.want)
		}
		if got := tt.o.IsValid(); got != tt.valid {
			t.Errorf("%v.IsValid() = %v, want %v", tt.o, got, tt.valid)
		}
	}
}

func TestSourceString(t *testing.T) {
	tests := []struct {
		s    output.Source
		want string
	}{
		{output.SourceInvalid, "invalid"},
		{output.SourceDecl, "decl"},
		{output.SourceScript, "script"},
		{output.SourceScript + 1, "invalid"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("Source(%d).String() = %q, want %q", uint8(tt.s), got, tt.want)
		}
	}
}
