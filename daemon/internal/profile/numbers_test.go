package profile

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
)

// Numbers that the strict JSON syntax allows but the rest of the pipeline
// cannot take, found by fuzzing:
//
//   - The schema validator converts every number with big.Rat.SetString,
//     which fails for exponents beyond about 1e6, and then panics. The
//     scanner rejects such numbers (and those that overflow a float64).
//   - The schema type "integer" accepts 2.0 and 1e2, which encoding/json
//     cannot decode into an int field. That is a diagnostic at the number.
const (
	wantNumberRange  = "number out of range; it has too many digits or an exponent that is too large"
	wantWholeNumber  = "write a whole number without a decimal point or exponent"
	wantIntegerRange = "number out of range; use a smaller whole number"
)

// noPanic runs f and reports a panic as a test error, so that one input that
// crashes does not hide the others.
func noPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: panic: %v", name, r)
		}
	}()
	f()
}

// documents holds one document of each kind, with %s where a number goes.
// The number is in a place that takes a number (a float for the profile and
// the config, an integer for the state).
var documents = []struct {
	name  string
	doc   string
	parse func(data []byte) Diagnostics
}{
	{"profile", `{"schemaVersion":1,"name":"n","analog":{"ls":{"deadzone":%s}},"layers":{"base":{}}}`,
		func(data []byte) Diagnostics { _, ds := Parse(data); return ds }},
	{"config", `{"schemaVersion":0,"devices":{"k":{"calibration":{"ls":{"deadzone":%s}}}}}`,
		func(data []byte) Diagnostics { _, ds := ParseConfig(data); return ds }},
	{"state", `{"schemaVersion":0,"devices":{"k":{"profiles":{"p":{"base":"base","cycles":{"a":%s}}}}}}`,
		func(data []byte) Diagnostics { _, ds := ParseState(data); return ds }},
}

// A number the validator cannot convert, or that is no float64, is a syntax
// error at the number in a profile, a config and a state, never a panic.
func TestNumberOutOfRange(t *testing.T) {
	for _, lit := range []string{
		"1E1000001", "1E-1000001", "-1E1000001", "1E400", "-1E400", "1e309", "1E+1000000000000",
		"1" + strings.Repeat("0", 400),
		"0." + strings.Repeat("0", 999) + "1", // 1001 characters: more than anyone writes, and slow to convert
		"0." + strings.Repeat("1", 1_000_000), // 9 s in big.Rat
	} {
		for _, d := range documents {
			name := d.name + " " + shorten(lit)
			doc := fmt.Sprintf(d.doc, lit)
			noPanic(t, name, func() {
				ds := d.parse([]byte(doc))
				if len(ds) != 1 || ds[0].Severity != SeverityError || ds[0].Message != wantNumberRange {
					t.Errorf("%s: diagnostics %v; want one error: %s", name, ds, wantNumberRange)
					return
				}
				if want := strings.Index(doc, lit); ds[0].Offset != want || ds[0].Pointer != "" {
					t.Errorf("%s: %+v; want a syntax error at offset %d", name, ds[0], want)
				}
			})
		}
	}
}

// Numbers at the edge of what a float64 holds are still numbers: the largest
// and the smallest, and those that underflow to 0, which is not an error.
func TestNumberExtremesAccepted(t *testing.T) {
	data := []byte(`[1E308, -1E308, 1.7976931348623157e308, 1E-400, 1e-999999, 5e-324, -0.0e0, 0E-400, ` +
		strings.Repeat("9", 300) + `, 0.` + strings.Repeat("0", 300) + `1, 0.` + strings.Repeat("0", 997) + `1]`) // the last has 1000 characters
	if _, serr := scan(data); serr != nil {
		t.Errorf("scan: %d: %s", serr.off, serr.msg)
	}
	for _, lit := range []string{"1E-400", "1e-999999", "5e-324"} {
		for _, d := range documents[:2] { // the state takes an integer
			doc := fmt.Sprintf(d.doc, lit)
			noPanic(t, d.name+" "+lit, func() {
				if ds := d.parse([]byte(doc)); len(ds) != 0 {
					t.Errorf("%s %s: %v", d.name, lit, ds)
				}
			})
		}
	}
	p, ds := Parse([]byte(fmt.Sprintf(documents[0].doc, "1E-400")))
	if p == nil || len(ds) != 0 || p.Analog.LS.Deadzone != 0 {
		t.Errorf("profile 1E-400: %v, %v", p, ds)
	}
	// Large numbers are checked against the schema like any other.
	for _, d := range documents[:2] {
		noPanic(t, d.name+" 1E308", func() {
			ds := d.parse([]byte(fmt.Sprintf(d.doc, "1E308")))
			if len(ds) != 1 || ds[0].Pointer == "" || ds[0].Message == wantNumberRange || strings.Contains(ds[0].Message, "internal") {
				t.Errorf("%s 1E308: %v; want one schema error", d.name, ds)
			}
		})
	}
	// An unbounded integer takes 1E308 as the whole number it is, but no int
	// holds it, and neither does a plain 20 digit number.
	for _, lit := range []string{"1E308", "1e100", "99999999999999999999", "9223372036854775808"} {
		doc := `{"schemaVersion":1,"name":"n","timing":{"holdMs":` + lit + `},"layers":{"base":{}}}`
		noPanic(t, "holdMs "+lit, func() {
			_, ds := Parse([]byte(doc))
			if len(ds) != 1 || ds[0].Message != wantIntegerRange || ds[0].Pointer != "/timing/holdMs" {
				t.Errorf("holdMs %s: %v; want %q at /timing/holdMs", lit, ds, wantIntegerRange)
			}
		})
	}
}

// wholeCase is a document with one number in an integer field.
type wholeCase struct {
	name string
	doc  string // contains "@" before the number, which is cut out
	ptr  string
	msg  string
}

func (c wholeCase) split() (doc string, off int) {
	off = strings.Index(c.doc, "@")
	return strings.Replace(c.doc, "@", "", 1), off
}

func checkWhole(t *testing.T, cases []wholeCase, parse func(data []byte) Diagnostics) {
	t.Helper()
	for _, c := range cases {
		doc, off := c.split()
		if !json.Valid(TrimBOM([]byte(doc))) {
			t.Fatalf("%s: the test document is no JSON: %q", c.name, doc)
		}
		noPanic(t, c.name, func() {
			ds := parse([]byte(doc))
			if len(ds) != 1 || ds[0].Severity != SeverityError || ds[0].Pointer != c.ptr || ds[0].Offset != off || ds[0].Message != c.msg {
				t.Errorf("%s: %q\n got %+v\nwant one error %q at offset %d: %s", c.name, doc, ds, c.ptr, off, c.msg)
			}
		})
	}
}

// A whole number written as 2.0 or 1e2 passes the schema ("integer") but not
// the decoder: it is an error at the number, wherever an int is.
func TestWholeNumberProfile(t *testing.T) {
	const keys = `"press":{"type":"keys","keys":"A"`
	var cases []wholeCase
	for _, lit := range []string{"1e2", "2.0", "1E+2", "0.0", "-0.0", "1.0e0", "5E-0", "100e-0"} {
		cases = append(cases, wholeCase{"timing.holdMs " + lit,
			`{"schemaVersion":1,"name":"n","timing":{"holdMs":@` + lit + `},"layers":{"base":{}}}`, "/timing/holdMs", wantWholeNumber})
	}
	cases = append(cases,
		wholeCase{"feedback", `{"schemaVersion":1,"name":"n","feedback":{"shortMs":@5.0},"layers":{"base":{}}}`, "/feedback/shortMs", wantWholeNumber},
		wholeCase{"script", `{"schemaVersion":1,"name":"n","script":{"entry":"main.ts","api":"` + CurrentScriptAPI + `","tickHz":@100.0},"layers":{"base":{}}}`, "/script/tickHz", wantWholeNumber},
		wholeCase{"analog", `{"schemaVersion":1,"name":"n","analog":{"rs":{"directionDelayMs":@1e1}},"layers":{"base":{}}}`, "/analog/rs/directionDelayMs", wantWholeNumber},
		wholeCase{"analog mouse", `{"schemaVersion":1,"name":"n","analog":{"ls":{"mode":"mouse","mouseSpeed":@900.0}},"layers":{"base":{}}}`, "/analog/ls/mouseSpeed", wantWholeNumber},
		// Inside a layer, which is decoded by its own UnmarshalJSON: the
		// position of the error must not come from the decoder.
		wholeCase{"binding timing", `{"schemaVersion":1,"name":"n","layers":{"base":{"south":{"timing":{"tapGapMs":@3.0},` + keys + `}}}}}`, "/layers/base/south/timing/tapGapMs", wantWholeNumber},
		wholeCase{"repeat", `{"schemaVersion":1,"name":"n","layers":{"base":{"south":{` + keys + `,"repeat":{"rateHz":@10.0}}}}}}`, "/layers/base/south/press/repeat/rateHz", wantWholeNumber},
		wholeCase{"sequence wait", `{"schemaVersion":1,"name":"n","layers":{"base":{"south":{"press":{"type":"sequence","steps":[{"keys":"A"},{"wait":@1e2}]}}}}}`, "/layers/base/south/press/steps/1/wait", wantWholeNumber},
		wholeCase{"common layer", `{"schemaVersion":1,"name":"n","common":{"east":{"tap":{"type":"sequence","steps":[{"wait":@2.0}]}}},"layers":{"base":{}}}`, "/common/east/tap/steps/0/wait", wantWholeNumber},
		wholeCase{"function key", `{"schemaVersion":1,"name":"n","functionKeys":{"start":{"south":{"press":{"type":"sequence","steps":[{"wait":@2.0}]}}}},"layers":{"base":{}}}`, "/functionKeys/start/south/press/steps/0/wait", wantWholeNumber},
		wholeCase{"layer after a good one", `{"schemaVersion":1,"name":"n","layers":{"base":{"a":{"timing":{"holdMs":5}}},"x":{"b":{"timing":{"holdMs":@5.0}}}}}`, "/layers/x/b/timing/holdMs", wantWholeNumber},
		wholeCase{"too large", `{"schemaVersion":1,"name":"n","timing":{"holdMs":@99999999999999999999},"layers":{"base":{}}}`, "/timing/holdMs", wantIntegerRange},
		wholeCase{"too large with an exponent", `{"schemaVersion":1,"name":"n","timing":{"holdMs":@1e100},"layers":{"base":{}}}`, "/timing/holdMs", wantIntegerRange},
	)
	checkWhole(t, cases, func(data []byte) Diagnostics { _, ds := Parse(data); return ds })

	// A BOM moves the offsets of the file, not the places in the text.
	doc := "\xEF\xBB\xBF" + `{"schemaVersion":1,"name":"n","timing":{"holdMs":2.0},"layers":{"base":{}}}`
	checkWhole(t, []wholeCase{{"bom", strings.Replace(doc, "2.0", "@2.0", 1), "/timing/holdMs", wantWholeNumber}},
		func(data []byte) Diagnostics { _, ds := Parse(data); return ds })

	// Whole numbers written plainly, and floats written as integers where a
	// float goes, are fine.
	for _, doc := range []string{
		`{"schemaVersion":1,"name":"n","timing":{"holdMs":100},"analog":{"ls":{"deadzone":0.0,"saturation":1.0}},"layers":{"base":{}}}`,
		`{"schemaVersion":1,"name":"n","timing":{"holdMs":-0},"layers":{"base":{}}}`,
		`{"schemaVersion":1.0,"name":"n","layers":{"base":{}}}`,
	} {
		if p, ds := Parse([]byte(doc)); p == nil || len(ds) != 0 {
			t.Errorf("%s: %v, %v", doc, p, ds)
		}
	}
}

// All the errors are reported, in file order, not only the first one the
// decoder meets.
func TestWholeNumberAllReported(t *testing.T) {
	doc := `{"schemaVersion":1,"name":"n","timing":{"holdMs":1e2,"tapGapMs":3},"feedback":{"shortMs":5.0},` +
		`"layers":{"base":{"a":{"timing":{"holdMs":7.0},"press":{"type":"sequence","steps":[{"wait":1e1}]}}}}}`
	_, ds := Parse([]byte(doc))
	var got []string
	for _, d := range ds {
		got = append(got, d.Pointer+" "+d.Message)
	}
	want := []string{
		"/timing/holdMs " + wantWholeNumber,
		"/feedback/shortMs " + wantWholeNumber,
		"/layers/base/a/timing/holdMs " + wantWholeNumber,
		"/layers/base/a/press/steps/0/wait " + wantWholeNumber,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The error is in the file it is in when the profile extends another.
func TestWholeNumberExtends(t *testing.T) {
	files := map[string]string{
		"parent": `{"schemaVersion":1,"name":"P","timing":{"holdMs":2.0},"layers":{"base":{}}}`,
		"good":   `{"schemaVersion":1,"name":"G","layers":{"base":{}}}`,
	}
	lib := &Library{ID: "child", Load: func(id string) ([]byte, error) {
		if s, ok := files[id]; ok {
			return []byte(s), nil
		}
		return nil, fs.ErrNotExist
	}}

	// The number is in the parent.
	child := `{"schemaVersion":1,"name":"C","extends":"parent","layers":{"base":{}}}`
	r, ds := Resolve([]byte(child), lib)
	if r != nil {
		t.Fatal("resolved")
	}
	var found bool
	for _, d := range ds {
		if d.Profile == "parent" && d.Pointer == "/timing/holdMs" && d.Message == wantWholeNumber &&
			d.Offset == strings.Index(files["parent"], "2.0") {
			found = true
		}
		if strings.Contains(d.Message, "internal") {
			t.Errorf("%+v", d)
		}
	}
	if !found {
		t.Errorf("no error at the parent's number: %v", ds)
	}

	// The number is in the child, and the merged text is what is checked.
	child = `{"schemaVersion":1,"name":"C","extends":"good","timing":{"holdMs":1e2},"layers":{"base":{}}}`
	r, ds = Resolve([]byte(child), lib)
	want := Diagnostic{Severity: SeverityError, Pointer: "/timing/holdMs", Offset: strings.Index(child, "1e2"), Message: wantWholeNumber}
	if r != nil || len(ds) != 1 || ds[0].Pointer != want.Pointer || ds[0].Offset != want.Offset || ds[0].Message != want.Message || ds[0].Profile != "" {
		t.Errorf("got %v (resolved %v); want %+v", ds, r != nil, want)
	}
}

func TestWholeNumberConfig(t *testing.T) {
	cases := []wholeCase{
		{"timing", `{"schemaVersion":0,"timing":{"holdMs":@1e2}}`, "/timing/holdMs", wantWholeNumber},
		{"timing 2.0", `{"schemaVersion":0,"enabled":false,"timing":{"multiTapMs":300,"tapGapMs":@20.0}}`, "/timing/tapGapMs", wantWholeNumber},
		{"output", `{"schemaVersion":0,"output":{"eventGapMs":@2.0}}`, "/output/eventGapMs", wantWholeNumber},
		{"panic combo", `{"schemaVersion":0,"safety":{"panic":{"standard":{"buttons":["back","start"],"holdMs":@1e3}}}}`, "/safety/panic/standard/holdMs", wantWholeNumber},
	}
	checkWhole(t, cases, func(data []byte) Diagnostics { _, ds := ParseConfig(data); return ds })

	// The repair leaves out the member that has the number and keeps the rest
	// (PRO-29): a combo goes as a whole, since it needs its holdMs.
	for _, c := range cases {
		member := strings.TrimSuffix(c.ptr, "/holdMs")
		if c.name != "panic combo" {
			member = c.ptr
		}
		doc, _ := c.split()
		noPanic(t, "repair "+c.name, func() {
			fixed, repair := RepairConfig([]byte(doc), func(string) bool { return true })
			if repair.Reset || !reflect.DeepEqual(repair.Members, []string{member}) {
				t.Errorf("%s: repair %+v of %s; want only %s reset", c.name, repair, doc, member)
			}
			got, ds := ParseConfig(fixed)
			if got == nil || len(ds) != 0 {
				t.Errorf("%s: repaired %s: %v", c.name, fixed, ds)
			}
			if c.name == "timing 2.0" && (got == nil || got.Enabled || got.Timing.MultiTapMs != 300) {
				t.Errorf("%s: the repair changed other settings: %+v", c.name, got)
			}
		})
	}
}

func TestWholeNumberState(t *testing.T) {
	const cycles = `{"schemaVersion":0,"devices":{"k":{"profiles":{"p":{"base":"base","cycles":{"a":1,"b":%s}}}}}}`
	checkWhole(t, []wholeCase{
		{"schemaVersion", `{"schemaVersion":@0.0}`, "/schemaVersion", wantWholeNumber},
		{"schemaVersion exponent", `{"schemaVersion":@0e3,"lastProfile":"paint"}`, "/schemaVersion", wantWholeNumber},
		{"cycles", fmt.Sprintf(cycles, "@1e0"), "/devices/k/profiles/p/cycles/b", wantWholeNumber},
		{"cycles 2.0", fmt.Sprintf(cycles, "@2.0"), "/devices/k/profiles/p/cycles/b", wantWholeNumber},
	}, func(data []byte) Diagnostics { _, ds := ParseState(data); return ds })
}

// The decoder is the same for the three documents; the walk that finds the
// numbers it refused must know every int in the types it decodes into, and the
// one type with its own decoder (wireLayer).
func TestWireTypesKnown(t *testing.T) {
	seen := map[reflect.Type]bool{}
	unmarshaler := reflect.TypeFor[interface{ UnmarshalJSON([]byte) error }]()
	var walk func(t reflect.Type, path string)
	walk = func(typ reflect.Type, path string) {
		if seen[typ] {
			return
		}
		seen[typ] = true
		if reflect.PointerTo(typ).Implements(unmarshaler) && typ != reflect.TypeFor[wireLayer]() && typ.PkgPath() == reflect.TypeFor[State]().PkgPath() {
			t.Errorf("%s: %s has its own UnmarshalJSON; teach the walk of whole numbers (wholenumber.go) to follow it", path, typ)
		}
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			walk(typ.Elem(), path+"[]")
		case reflect.Struct:
			if typ == reflect.TypeFor[wireLayer]() {
				walk(reflect.TypeFor[wireBinding](), path)
				return
			}
			for i := range typ.NumField() {
				f := typ.Field(i)
				if f.IsExported() {
					walk(f.Type, path+"."+f.Name)
				}
			}
		case reflect.Int, reflect.String, reflect.Bool, reflect.Float64, reflect.Interface, reflect.Uint8:
		default:
			t.Errorf("%s: kind %s of %s is not one the walk knows (numbers must be int or float64)", path, typ.Kind(), typ)
		}
	}
	for _, v := range []any{wireProfile{}, wireConfig{}, State{}} {
		walk(reflect.TypeOf(v), reflect.TypeOf(v).Name())
	}
}
