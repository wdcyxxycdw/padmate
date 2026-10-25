package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

const keysUsage = `usage: padmatectl keys <subcommand> [arguments]

subcommands:
  parse [--json] <spec>...  print the canonical form of each key spec
  list                      print the key table with US scan and VK codes
`

const keysParseUsage = `usage: padmatectl keys parse [--json] <spec>...

Parses each key spec, such as "ctrl+shift+z", and prints its canonical form
on its own line. Exits 1 if any spec is invalid.

flags:
  --json  print one JSON object per spec, with its modifiers, key, kind
          and US scan code, extended flag and VK code; a mouse key (a
          wheel notch) has no scan code and VK, so it has "mouse", the
          input it stands for, in place of "us"
`

const keysListUsage = `usage: padmatectl keys list

Prints every key name with its kind, US scan code, VK code and aliases.
Mouse keys, such as WheelUp, have no scan code and VK: they show "-". Media
and volume keys, such as VolumeUp, have the kind "media" and an E0 scan code.
`

// runKeys implements "padmatectl keys".
func runKeys(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, keysUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, keysUsage)
		return 0
	case "parse":
		return runKeysParse(args[1:], stdout, stderr)
	case "list":
		return runKeysList(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "padmatectl keys: unknown subcommand %q\n\n", args[0])
	fmt.Fprint(stderr, keysUsage)
	return 2
}

// parseFlags parses the flags of a subcommand. When it returns false
// the subcommand is done and should exit with the returned code: 0 after
// printing help, 2 after a flag error.
func parseFlags(fs *flag.FlagSet, args []string, usage string, stdout, stderr io.Writer) (int, bool) {
	fs.SetOutput(stderr)
	fs.Usage = func() {} // usage is printed below, to the right stream
	err := fs.Parse(args)
	switch {
	case err == nil:
		return 0, true
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(stdout, usage)
		return 0, false
	default:
		fmt.Fprint(stderr, usage)
		return 2, false
	}
}

func runKeysParse(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("keys parse", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print one JSON object per spec")
	if code, ok := parseFlags(fs, args, keysParseUsage, stdout, stderr); !ok {
		return code
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "padmatectl keys parse: no key specs given")
		fmt.Fprint(stderr, keysParseUsage)
		return 2
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	status := 0
	for _, spec := range fs.Args() {
		c, err := keys.Parse(spec)
		var out any
		if err != nil {
			status = 1
			printKeySpecError(stderr, "padmatectl keys parse: ", spec, err)
			out = keySpecErrorJSON(spec, err)
		} else {
			out = chordJSON(spec, c)
		}
		switch {
		case *asJSON:
			if err := enc.Encode(out); err != nil {
				fmt.Fprintln(stderr, "padmatectl keys parse:", err)
				return 1
			}
		case err == nil:
			fmt.Fprintln(stdout, c)
		}
	}
	return status
}

// printKeySpecError reports an invalid spec after prefix, such as
// "padmatectl keys parse: ", followed by a caret line that points at the
// offending token in the echoed spec.
func printKeySpecError(w io.Writer, prefix, spec string, err error) {
	fmt.Fprintf(w, "%s%s: %v\n", prefix, spec, err)
	var pe *keys.ParseError
	if !errors.As(err, &pe) || spec == "" || strings.ContainsAny(spec, "\r\n") {
		return
	}
	fmt.Fprintln(w, caretLine(len(prefix), spec, pe.Offset, pe.Len))
}

// caretLine returns a line of "^" under s[off:off+n], which is printed
// after indent columns. Tabs in s are copied so the carets stay aligned.
func caretLine(indent int, s string, off, n int) string {
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", indent))
	for _, r := range s[:off] {
		if r == '\t' {
			b.WriteByte('\t')
		} else {
			b.WriteByte(' ')
		}
	}
	b.WriteString(strings.Repeat("^", max(1, utf8.RuneCountInString(s[off:off+n]))))
	return b.String()
}

// usJSON is the US-layout mapping of a key in "keys parse --json".
type usJSON struct {
	Scan  uint8  `json:"scan"`
	Ext   bool   `json:"ext"`
	E1    bool   `json:"e1,omitempty"`
	VK    uint16 `json:"vk,omitempty"`
	Shift bool   `json:"shift,omitempty"`
}

// chordJSONObject is one parsed spec in "keys parse --json". A mouse key has
// Mouse, the name of its input, and no US mapping.
type chordJSONObject struct {
	Spec      string   `json:"spec"`
	Canonical string   `json:"canonical"`
	Modifiers []string `json:"modifiers"`
	Key       string   `json:"key"`
	Kind      string   `json:"kind"`
	US        *usJSON  `json:"us,omitempty"`
	Mouse     string   `json:"mouse,omitempty"`
}

// errorJSONObject is one invalid spec in "keys parse --json".
type errorJSONObject struct {
	Spec   string `json:"spec"`
	Error  string `json:"error"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

func chordJSON(spec string, c keys.Chord) chordJSONObject {
	mods := make([]string, 0, 4)
	for _, m := range c.Mods().Keys() {
		mods = append(mods, m.String())
	}
	k := c.Key()
	out := chordJSONObject{
		Spec:      spec,
		Canonical: c.String(),
		Modifiers: mods,
		Key:       k.String(),
		Kind:      k.Kind().String(),
	}
	if m, ok := k.Mouse(); ok {
		out.Mouse = m.String()
		return out
	}
	us := k.US()
	out.US = &usJSON{Scan: us.Scan, Ext: us.Ext, E1: us.E1, VK: us.VK, Shift: us.Shift}
	return out
}

func keySpecErrorJSON(spec string, err error) errorJSONObject {
	out := errorJSONObject{Spec: spec, Error: err.Error()}
	var pe *keys.ParseError
	if errors.As(err, &pe) {
		out.Offset, out.Length = pe.Offset, pe.Len
	}
	return out
}

func runKeysList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("keys list", flag.ContinueOnError)
	if code, ok := parseFlags(fs, args, keysListUsage, stdout, stderr); !ok {
		return code
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "padmatectl keys list: unexpected argument %q\n", fs.Arg(0))
		fmt.Fprint(stderr, keysListUsage)
		return 2
	}
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tKIND\tSCAN\tVK\tALIASES")
	for _, k := range keys.Table() {
		scan, vk := "-", "-" // a mouse key is no keyboard key
		if k.Kind() != keys.KindMouse {
			us := k.US()
			scan, vk = us.ScanString(), fmt.Sprintf("0x%02X", us.VK)
			if us.Shift {
				vk = "Shift+" + vk
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", k, k.Kind(), scan, vk, strings.Join(k.Aliases(), ", "))
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintln(stderr, "padmatectl keys list:", err)
		return 1
	}
	// Rows without aliases end in padding; trim it.
	for line := range strings.Lines(buf.String()) {
		fmt.Fprintln(stdout, strings.TrimRight(line, " \n"))
	}
	return 0
}
