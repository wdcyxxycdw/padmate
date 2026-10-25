package compile

import _ "embed"

//go:embed lib.padmate-goja.d.ts
var gojaLib []byte

// GojaLib returns the generated TypeScript standard library declarations.
// Callers should treat the returned bytes as read-only.
func GojaLib() []byte { return gojaLib }
