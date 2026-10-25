// Package compile bundles a padmate TypeScript script into one IIFE script
// for goja, with esbuild (SCRIPTING §3).
//
// Build resolves imports through a plugin that serves the virtual padmate
// modules, rejects Node.js built-in modules and imports outside the script
// root, and turns top-level await into a clear diagnostic. The esbuild
// "supported" table comes from goja_features.json, which the gojafeatures
// tool generates by probing the linked goja; lib.padmate-goja.d.ts, generated
// with it, is the TypeScript standard library to type-check scripts against.
//
// BuildOptions.Macro builds a macro (SCR-50, ADR-0055), a top-level file
// of a macros folder, the build's root (the script library's, SCR-51): its
// padmate module has only the members of MacroExports, read from the
// macro's own bridge, and using another one is a build error. A legacy
// entry cannot be in its profile's macros folder.
//
// The package is cross-platform. Build is stateless; Cache, BuildCached
// and BuildMacros provide optional successful-bundle caching keyed by the
// source hash, API, kind of build and esbuild version, one bundle for each
// entry.
package compile

//go:generate go run ../../../tools/gojafeatures
