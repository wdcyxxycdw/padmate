// Command schemacopy copies the canonical profile schema into the profile
// package (PRO-02). The schema's single source is schema/profile.schema.json
// at the repository root (ADR-0008), which go:embed cannot reach from the
// daemon module. `task gen` runs this command through the go:generate
// directive in internal/profile:
//
//	go run ../../tools/schemacopy -src ../../../schema/profile.schema.json -dst profile.schema.json
//
// The copy is byte-identical. The source must be valid JSON, and the
// destination is rewritten only when it differs. CI's go generate check and
// the profile package's TestSchemaCopyCurrent catch a stale copy.
//
// It prints nothing on success and exits 0. It exits 1 when reading,
// checking or writing fails and 2 on bad flags.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("schemacopy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	src := fs.String("src", "", "file to copy")
	dst := fs.String("dst", "", "file to write")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "schemacopy: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if *src == "" || *dst == "" {
		fmt.Fprintln(stderr, "schemacopy: -src and -dst are required")
		return 2
	}
	if err := copyFile(*src, *dst); err != nil {
		fmt.Fprintf(stderr, "schemacopy: %v\n", err)
		return 1
	}
	return 0
}

// copyFile writes the bytes of src to dst unless dst already holds them.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("%s is not valid JSON: %w", src, err)
	}
	if old, err := os.ReadFile(dst); err == nil && bytes.Equal(old, data) {
		return nil
	}
	return os.WriteFile(dst, data, 0o644)
}
