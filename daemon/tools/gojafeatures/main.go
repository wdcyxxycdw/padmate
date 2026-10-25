// Command gojafeatures probes the linked goja for the JavaScript features and
// standard library that padmate scripts can use (SPK-03). `task gen` runs it
// through the go:generate directive in internal/script/compile:
//
//	go run ../../../tools/gojafeatures [-dir .]
//
// It writes two files into -dir:
//
//   - goja_features.json: esbuild's BuildOptions.Supported table. Each esbuild
//     feature is decided by a small script run in a fresh goja Runtime; module
//     features that have no meaning in a classic IIFE script are false, with
//     the reason in "notes".
//   - lib.padmate-goja.d.ts: /// <reference lib> lines for the TypeScript lib
//     components whose members all exist in goja, with comments listing the
//     partial and excluded components and the missing members.
//
// The output depends only on the goja and esbuild versions in go.mod, so it is
// byte-identical on every machine. TestGeneratedFilesCurrent fails when the
// committed files differ from what the current goja produces.
//
// It prints nothing on success and exits 0. It exits 1 when probing or
// writing fails and 2 on bad flags.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	featuresFileName = "goja_features.json"
	libFileName      = "lib.padmate-goja.d.ts"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("gojafeatures", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", ".", "directory to write the generated files into")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "gojafeatures: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	files, err := generate()
	if err != nil {
		fmt.Fprintf(stderr, "gojafeatures: %v\n", err)
		return 1
	}
	for _, name := range []string{featuresFileName, libFileName} {
		path := filepath.Join(*dir, name)
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			fmt.Fprintf(stderr, "gojafeatures: %v\n", err)
			return 1
		}
	}
	return 0
}

// generate probes goja and returns the contents of both files by name.
func generate() (map[string][]byte, error) {
	v, err := buildVersions()
	if err != nil {
		return nil, err
	}
	results := probeFeatures(features)
	supported := make(map[string]bool, len(results))
	for name, r := range results {
		supported[name] = r.supported
	}
	if err := checkEsbuild(supported); err != nil {
		return nil, err
	}
	js, err := renderFeatures(v, results)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		featuresFileName: js,
		libFileName:      renderLib(v, probeLibs(libComponents)),
	}, nil
}
