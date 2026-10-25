// Package profile loads and validates padmate profiles (PRO-01, PRO-02,
// PRO-04).
//
// Parse checks a profile in three stages. A stage runs only if the previous
// ones found no errors; warnings never stop it.
//
//  1. Syntax: strict JSON read by the package's own scanner, which also
//     indexes every value by JSON Pointer with its byte offset. Comments,
//     trailing commas, duplicate keys and invalid UTF-8 are errors; a
//     leading UTF-8 byte order mark is skipped.
//  2. Schema: the document is validated against the embedded JSON Schema,
//     and each leaf of the validator's error tree becomes one diagnostic
//     with a message of its own, so one mistake gives one diagnostic.
//  3. Semantics: the document is decoded and resolved into a Profile. Key
//     specs are parsed, a control bound under two names is an error, layer
//     names and the targets of layer actions must exist, every action obeys
//     the action rules that engine.Compile checks too (ARCHITECTURE §7.7),
//     and a binding without an action is a warning. Back and start are
//     the function keys (MAP-27, ADR-0069): a binding on them in a layer or
//     in common is dropped with a warning, and the sets of bindings that
//     are used while one is held, FunctionKeys, are resolved like layers.
//
// A profile of the library may extend another (PRO-19, ADR-0070): Resolve
// merges the chain of parents into one document first (extends.go) and checks
// that, so a merged profile passes the same three stages, and a diagnostic
// is reported in the file of the profile that has the problem. Parse is
// Resolve without a library, where extends is an error.
//
// Every Diagnostic has a JSON Pointer and a position: a byte offset from
// the start of the file (byte order mark included), a 1-based line and a
// 1-based column counted in Unicode code points, so that a Chinese label
// before an error does not shift it.
//
// The profile format is defined by the JSON Schema in
// schema/profile.schema.json at the repository root, its single source
// (ADR-0008). go:embed cannot reach outside the daemon module, so go
// generate (task gen) copies it byte for byte to profile.schema.json in this
// directory, which the package embeds. The copy is a generated file: edit the
// root schema and run task gen. TestSchemaCopyCurrent and CI's go generate
// check catch a stale copy.
//
// The schema is closed: it accepts only what the daemon can execute and
// rejects everything else, naming the milestone that adds it where one
// does. Later milestones widen it. The action rules, added in M2's audit,
// reject some profiles the schema accepted before, whose tap-class
// gestures could hold a key that nothing released (MAP-07).
//
// The package is pure: no platform dependencies, no clock.
package profile

//go:generate go run ../../tools/schemacopy -src ../../../schema/profile.schema.json -dst profile.schema.json
