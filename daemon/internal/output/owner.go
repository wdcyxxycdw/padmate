package output

import "fmt"

// Owner identifies who holds a key (ARCHITECTURE §8.2): a device slot, the
// slot's profile generation, what issued the command and the press cycle.
// The zero Owner is invalid: every Arbiter command rejects it, which
// catches owners that were never filled in.
type Owner struct {
	Slot   uint8  // device slot
	Gen    uint32 // profile generation of the slot
	Source Source // what issued the command
	Cycle  uint64 // press cycle; 0 when not tied to one (e.g. script key.down)
}

// IsValid reports whether o has a valid Source.
func (o Owner) IsValid() bool { return o.Source.IsValid() }

// String formats o as slot/gen/source/cycle, such as "0/3/decl/17".
func (o Owner) String() string {
	return fmt.Sprintf("%d/%d/%s/%d", o.Slot, o.Gen, o.Source, o.Cycle)
}

// Source is what issued an Arbiter command.
type Source uint8

const (
	SourceInvalid Source = iota // the zero Source, rejected
	SourceDecl                  // a declarative mapping
	SourceScript                // a script
)

// IsValid reports whether s is SourceDecl or SourceScript.
func (s Source) IsValid() bool { return s == SourceDecl || s == SourceScript }

// String returns "decl", "script", or "invalid" for any other value.
func (s Source) String() string {
	switch s {
	case SourceDecl:
		return "decl"
	case SourceScript:
		return "script"
	}
	return "invalid"
}
