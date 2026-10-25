package profile

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"
)

const (
	wholeNumberMessage  = "write a whole number without a decimal point or exponent"
	integerRangeMessage = "number out of range; use a smaller whole number"
)

// decodeWire decodes the document in data into v, a pointer to a wire type.
// The document passed the schema and root is its scanned tree. The result is
// nil when it decodes; otherwise it holds errors at the numbers that the
// schema took for integers but no int holds (see intProblems), or an internal
// error when that is not the reason. Offsets are those of data.
func decodeWire(data []byte, root *node, v any) Diagnostics {
	err := json.Unmarshal(data[bomLen(data):], v)
	if err == nil {
		return nil
	}
	if ds := intProblems(root, reflect.TypeOf(v).Elem()); len(ds) > 0 {
		return ds
	}
	return Diagnostics{internalError(fmt.Errorf("decoding a document that passed the schema: %w", err))}
}

var (
	wireLayerType   = reflect.TypeFor[wireLayer]()
	wireBindingType = reflect.TypeFor[wireBinding]()
)

// intProblems returns an error for each number in root, a document that
// passed the schema, that the decoder cannot put into the int of type t, the
// type of the document, which are:
//
//   - A whole number written with a decimal point or an exponent (2.0, 1e2).
//     The schema type "integer" is about the value and accepts it; encoding/json
//     reads the text and refuses it.
//   - A whole number that does not fit an int, for a setting with no maximum.
//
// The numbers are found by walking the tree along the Go type, not from the
// error of the decoder: it reports only the first problem, and when it fails
// inside a type with its own UnmarshalJSON (wireLayer), its Offset is relative
// to the text that type was given. The walk follows the json tags and the one
// custom decoder, wireLayer (TestWireTypesKnown checks that there is no other).
func intProblems(root *node, t reflect.Type) Diagnostics {
	var ds Diagnostics
	var walk func(n *node, ptr string, t reflect.Type)
	walk = func(n *node, ptr string, t reflect.Type) {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		switch t.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if lit, ok := n.val.(json.Number); ok {
				if msg := intProblem(string(lit), t); msg != "" {
					ds = append(ds, Diagnostic{Severity: SeverityError, Pointer: ptr, Offset: n.start, Message: msg})
				}
			}
		case reflect.Struct:
			if t == wireLayerType { // its members are the color and the bindings
				for _, m := range n.order {
					if m.name != "color" {
						walk(m, childPointer(ptr, m.name), wireBindingType)
					}
				}
				return
			}
			for i := range t.NumField() {
				f := t.Field(i)
				name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
				if name == "" {
					name = f.Name
				}
				if m := n.members[name]; m != nil && f.IsExported() && name != "-" {
					walk(m, childPointer(ptr, name), f.Type)
				}
			}
		case reflect.Map:
			for _, m := range n.order {
				walk(m, childPointer(ptr, m.name), t.Elem())
			}
		case reflect.Slice:
			if t.Elem().Kind() == reflect.Uint8 { // json.RawMessage
				return
			}
			for i, e := range n.order {
				walk(e, ptr+"/"+strconv.Itoa(i), t.Elem())
			}
		}
	}
	walk(root, "", t)
	return ds
}

// intProblem returns the message for the number lit, which the scanner
// accepted, in an int of type t, or "" if it decodes. A number too large for
// the type is reported first: writing it without an exponent would not help.
func intProblem(lit string, t reflect.Type) string {
	r, ok := new(big.Rat).SetString(lit)
	if !ok || !r.IsInt() {
		return wholeNumberMessage // not expected: the schema wants an integer
	}
	if n := r.Num(); !n.IsInt64() || reflect.New(t).Elem().OverflowInt(n.Int64()) {
		return integerRangeMessage
	}
	if strings.ContainsAny(lit, ".eE") {
		return wholeNumberMessage
	}
	return ""
}
