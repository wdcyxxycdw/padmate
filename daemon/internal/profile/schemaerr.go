package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// Schema locations, as fragments of a ValidationError's SchemaURL, that
// get their own messages. TestSchemaMessages checks that each exists.
const (
	schemaRoot         = ""
	schemaVersionProp  = "/properties/schemaVersion"
	schemaScript       = "/$defs/script"
	schemaLayers       = "/properties/layers"
	schemaLayer        = "/$defs/layer"
	schemaFunctionKeys = "/$defs/functionKeys"
	schemaFunctionSet  = "/$defs/functionSet"
	schemaBinding      = "/$defs/binding"
	schemaAction       = "/$defs/action"
	schemaActionType   = "/$defs/action/properties/type"
	schemaKeysAction   = "/$defs/keysAction"
	schemaKeys         = "/$defs/keysAction/properties/keys"
	schemaMode         = "/$defs/keysAction/properties/mode"
	schemaSequenceStep = "/$defs/sequenceStep"
)

// validate checks the document against the embedded schema and turns each
// leaf of the error tree into one diagnostic.
func validate(root *node) Diagnostics {
	s, err := compiledSchema()
	if err != nil {
		return Diagnostics{internalError(err)}
	}
	err = s.Validate(root.val)
	if err == nil {
		return nil
	}
	var verr *jsonschema.ValidationError
	if !errors.As(err, &verr) {
		return Diagnostics{internalError(err)}
	}
	f := flattener{root: root}
	for _, e := range leaves(verr, nil) {
		f.add(e)
	}
	return f.ds
}

func internalError(err error) Diagnostic {
	return Diagnostic{Severity: SeverityError, Message: "internal error: " + err.Error()}
}

// leaves appends the errors below the wrappers that only group others: the
// whole schema, a group at one location, a $ref and an allOf (the if/then
// of the action union shows up as allOf and $ref).
func leaves(e *jsonschema.ValidationError, out []*jsonschema.ValidationError) []*jsonschema.ValidationError {
	switch e.ErrorKind.(type) {
	case *kind.Schema, *kind.Group, *kind.Reference, *kind.AllOf:
		for _, c := range e.Causes {
			out = leaves(c, out)
		}
		return out
	}
	return append(out, e)
}

type flattener struct {
	root *node
	ds   Diagnostics
}

// at returns the node at loc, or its nearest ancestor if a validator path
// ever misses the index, and that node's pointer.
func (f *flattener) at(loc []string) (*node, string) {
	n := f.root
	for i, t := range loc {
		c := n.lookup([]string{t})
		if c == nil {
			return n, pointer(loc[:i])
		}
		n = c
	}
	return n, pointer(loc)
}

func (f *flattener) errorAt(off int, ptr, msg string) {
	f.ds = append(f.ds, Diagnostic{Severity: SeverityError, Pointer: ptr, Offset: off, Message: msg})
}

func (f *flattener) add(e *jsonschema.ValidationError) {
	n, ptr := f.at(e.InstanceLocation)
	_, where, _ := strings.Cut(e.SchemaURL, "#")
	switch k := e.ErrorKind.(type) {
	case *kind.AdditionalProperties:
		props := slices.Clone(k.Properties)
		slices.SortFunc(props, func(a, b string) int { return n.keyOffset(a) - n.keyOffset(b) })
		misplaced := false
		for _, p := range props {
			off, pp := n.start, ptr
			if m := n.members[p]; m != nil {
				off, pp = m.key, childPointer(ptr, p)
			}
			if where == schemaBinding && slices.Contains(actionProperties, p) {
				if misplaced {
					continue // one hint is enough for an action written without "press"
				}
				misplaced = true
			}
			f.errorAt(off, pp, unknownProperty(where, p))
		}
	case *kind.Required:
		for _, name := range k.Missing {
			f.missing(n, ptr, where, name)
		}
	case *kind.Type:
		f.errorAt(n.start, ptr, typeMessage(where, k, n.val))
	case *kind.Enum:
		f.errorAt(n.start, ptr, enumMessage(where, k))
	case *kind.Const:
		f.errorAt(n.start, ptr, constMessage(where, k))
	case *kind.FalseSchema:
		// A property a branch of the schema forbids: the member that does not
		// belong to the action's operation.
		off := n.start
		if n.key >= 0 {
			off = n.key
		}
		f.errorAt(off, ptr, falseSchemaMessage(e.InstanceLocation))
	case *kind.MinItems:
		f.errorAt(n.start, ptr, minItemsMessage(e.InstanceLocation, fallbackMessage(e)))
	case *kind.MaxItems:
		f.errorAt(n.start, ptr, maxItemsMessage(e.InstanceLocation, k, fallbackMessage(e)))
	case *kind.Minimum:
		f.errorAt(n.start, ptr, rangeMessage(e.InstanceLocation, fallbackMessage(e)))
	case *kind.Maximum:
		f.errorAt(n.start, ptr, rangeMessage(e.InstanceLocation, fallbackMessage(e)))
	case *kind.MinProperties:
		f.errorAt(n.start, ptr, minPropertiesMessage(where, fallbackMessage(e)))
	case *kind.MaxProperties:
		f.errorAt(n.start, ptr, maxPropertiesMessage(where, fallbackMessage(e)))
	case *kind.MinLength:
		msg := fallbackMessage(e)
		if k.Want == 1 && len(e.InstanceLocation) > 0 {
			msg = e.InstanceLocation[len(e.InstanceLocation)-1] + " must not be empty"
		}
		f.errorAt(n.start, ptr, msg)
	default:
		f.errorAt(n.start, ptr, fallbackMessage(e))
	}
}

// minItemsMessage words the diagnostic for an array shorter than the schema
// wants: the steps of a sequence.
func minItemsMessage(loc []string, fallback string) string {
	if isMember(loc, "steps") {
		return "a sequence needs at least one step"
	}
	return fallback
}

// maxItemsMessage words the diagnostic for an array longer than the schema
// allows: the steps of a sequence.
func maxItemsMessage(loc []string, k *kind.MaxItems, fallback string) string {
	if isMember(loc, "steps") {
		return fmt.Sprintf("a sequence has at most %d steps, not %d", k.Want, k.Got)
	}
	return fallback
}

// rangeMessage words the diagnostic for a number outside the range the schema
// allows: the wait of a step.
func rangeMessage(loc []string, fallback string) string {
	if isMember(loc, "wait") {
		return "a wait is 1 to 10000 milliseconds"
	}
	return fallback
}

// isMember reports whether the instance at loc is the member name of an object.
func isMember(loc []string, name string) bool {
	return len(loc) > 0 && loc[len(loc)-1] == name
}

// minPropertiesMessage words the diagnostic for an object with fewer members
// than the schema wants: a step of a sequence with none.
func minPropertiesMessage(where, fallback string) string {
	if where == schemaSequenceStep {
		return `a step needs one of "keys", "wait" or "text"`
	}
	return fallback
}

// maxPropertiesMessage words the diagnostic for an object with more members
// than the schema allows: a step of a sequence with two.
func maxPropertiesMessage(where, fallback string) string {
	if where == schemaSequenceStep {
		return `a step has only one of "keys", "wait" and "text": write one step for each`
	}
	return fallback
}

// falseSchemaMessage words the diagnostic for the member at loc that a branch
// of the schema forbids: the position of a precision action and the factor of
// a jump.
func falseSchemaMessage(loc []string) string {
	if len(loc) == 0 {
		return "this value is not allowed here"
	}
	switch name := loc[len(loc)-1]; name {
	case "x", "y":
		return name + ` belongs to a "jump" action; a "precision" action takes factor`
	case "factor":
		return `factor belongs to a "precision" action; a "jump" action takes x and y`
	default:
		return "unknown property " + quote(name)
	}
}

// ownerSchema returns where without the branch inside the schema object that
// owns it, such as /allOf/1/then: the object whose properties and closedness
// apply to the instance.
func ownerSchema(where string) string {
	if i := strings.Index(where, "/allOf/"); i >= 0 {
		return where[:i]
	}
	return where
}

// missing reports a missing required property, unless the object has a
// misspelling of it: then the misspelled key gets the diagnostic, from the
// schema when the object is closed and from here otherwise.
func (f *flattener) missing(n *node, ptr, where, name string) {
	sch := schemaAt(ownerSchema(where))
	known := propertyNames(sch)
	for _, m := range n.order {
		if slices.Contains(known, m.name) || len(suggest(m.name, []string{name})) == 0 {
			continue
		}
		if !closed(sch) {
			f.errorAt(m.key, childPointer(ptr, m.name), fmt.Sprintf("unknown property %s; did you mean %s?", quote(m.name), quote(name)))
		}
		return
	}
	switch where {
	case schemaLayers:
		if len(n.order) > 0 {
			return // each unknown layer already says that only base exists
		}
		f.errorAt(n.start, ptr, "missing layer "+quote(name))
	case schemaRoot:
		if name == "schemaVersion" {
			f.errorAt(n.start, ptr, fmt.Sprintf(`missing property "schemaVersion"; add "schemaVersion": %d`, CurrentSchema))
			return
		}
		f.errorAt(n.start, ptr, "missing property "+quote(name))
	case schemaAction:
		if name == "type" {
			f.errorAt(n.start, ptr, `missing property "type"; add "type": "keys"`)
			return
		}
		f.errorAt(n.start, ptr, "missing property "+quote(name))
	default:
		f.errorAt(n.start, ptr, "missing property "+quote(name))
	}
}

// Hints for names that later milestones add, so a profile written for a
// newer padmate says what is missing rather than only "unknown".
var (
	laterProperties = map[string]map[string]string{
		schemaRoot:    {},
		schemaBinding: {},
		schemaKeysAction: {
			"label": `label belongs to the binding, next to "press"`,
		},
	}
	// actionProperties are the properties of an action, which a binding
	// written without "press" has instead.
	actionProperties = []string{"type", "keys", "mode"}
	// controlNicknames maps names printed on pads, and SDL's axis names, to
	// padmate's position names.
	controlNicknames = map[string]string{
		"cross": "south", "circle": "east", "square": "west", "triangle": "north",
		"view": "back", "share": "back", "create": "back", "minus": "back",
		"menu": "start", "options": "start", "plus": "start",
		"home": "guide", "ps": "guide", "xbox": "guide",
		"l1": "leftshoulder", "r1": "rightshoulder", "l": "leftshoulder", "r": "rightshoulder",
		"l2": "lt", "r2": "rt", "zl": "lt", "zr": "rt", "lefttrigger": "lt", "righttrigger": "rt",
		"ls": "leftstick", "rs": "rightstick",
		"up": "dpad.up", "down": "dpad.down", "left": "dpad.left", "right": "dpad.right",
	}
)

// unknownProperty words the diagnostic for property p that the schema at
// where does not allow.
func unknownProperty(where, p string) string {
	switch where {
	case schemaLayers:
		if s := suggest(p, []string{"base"}); len(s) > 0 {
			return "unknown layer " + quote(p) + didYouMean(s)
		}
		return "unknown layer " + quote(p) + `; only "base" until layers arrive in M3`
	case schemaLayer:
		return "unknown control " + quote(p) + controlHint(p, schemaLayer)
	case schemaFunctionKeys:
		return unknownFunctionKey(p)
	case schemaFunctionSet:
		return functionSetProperty(p)
	}
	if where == schemaBinding && slices.Contains(actionProperties, p) {
		return "unknown property " + quote(p) + `; put the action inside "press": {"press": {"type": "keys", ...}}`
	}
	if hint, ok := laterProperties[where][p]; ok {
		return "unknown property " + quote(p) + "; " + hint
	}
	return "unknown property " + quote(p) + didYouMean(suggest(p, propertyNames(schemaAt(where))))
}

// functionKeyNamed returns the function key, "back" or "start", that name
// means, in any case: its own name, an alias (select) or a name printed on a
// pad (View, Menu, Options).
func functionKeyNamed(name string) (string, bool) {
	name = strings.ToLower(name)
	if c, ok := control.Parse(name); ok && c.IsFunctionKey() {
		return c.String(), true
	}
	if c, ok := controlNicknames[name]; ok && (c == "back" || c == "start") {
		return c, true
	}
	return "", false
}

// unknownFunctionKey words the diagnostic for member p of functionKeys, which
// has only back and start: a name printed on a pad gets the key it means (not
// the nearest spelling, which could be the other one), a typo its correction,
// and any other name the two that exist.
func unknownFunctionKey(p string) string {
	if name, ok := functionKeyNamed(p); ok {
		return "unknown property " + quote(p) + didYouMean([]string{name})
	}
	if s := suggest(p, propertyNames(schemaAt(schemaFunctionKeys))); len(s) > 0 {
		return "unknown property " + quote(p) + didYouMean(s)
	}
	return "unknown property " + quote(p) + `; only "back" and "start" are function keys`
}

// functionSetProperty words the diagnostic for property p that a function key
// set does not allow: the function keys themselves (and the names printed on
// them, which a layer would hint at), a color, which only a layer has, or a
// name that is no control.
func functionSetProperty(p string) string {
	if c, ok := control.Parse(p); ok && c.IsFunctionKey() {
		return quote(p) + " is a function key (ADR-0069); a function key set cannot bind it"
	}
	if c, ok := controlNicknames[strings.ToLower(p)]; ok && (c == "back" || c == "start") {
		return quote(p) + " is a function key (ADR-0069); a function key set cannot bind it"
	}
	if p == "color" {
		return "a function key set has no color: it is not a layer"
	}
	return "unknown control " + quote(p) + controlHint(p, schemaFunctionSet)
}

// controlHint words the hint after an unknown control name p of the layer, or
// the function key set, at where.
func controlHint(p, where string) string {
	switch {
	case strings.HasPrefix(p, "ls.") || strings.HasPrefix(p, "rs."):
		return "; stick directions arrive in M4"
	case slices.Contains([]string{"dpad.upleft", "dpad.upright", "dpad.downleft", "dpad.downright"}, p):
		return "; d-pad diagonals arrive in M4"
	}
	// A printed name is not a typo: "l1" is the left shoulder, not "lt",
	// the nearest name.
	if c, ok := controlNicknames[strings.ToLower(p)]; ok {
		return didYouMean([]string{c})
	}
	return didYouMean(suggest(p, propertyNames(schemaAt(where))))
}

func typeMessage(where string, k *kind.Type, v any) string {
	want := make([]string, len(k.Want))
	for i, w := range k.Want {
		want[i] = withArticle(w)
	}
	got := withArticle(k.Got)
	s, isString := v.(string)
	switch where {
	case schemaRoot:
		return "a profile is a JSON object, got " + got
	case schemaBinding:
		msg := "expected a binding object, got " + got
		if isString {
			msg += `; write {"press": {"type": "keys", "keys": ` + quote(s) + `}}`
		}
		return msg
	case schemaAction, schemaKeysAction:
		msg := "expected an action object, got " + got
		if isString {
			msg += `; write {"type": "keys", "keys": ` + quote(s) + `}`
		}
		return msg
	}
	msg := "expected " + orList(want) + ", got " + got
	if where == schemaKeys && k.Got == "array" {
		msg += "; key sequences arrive in M2"
	}
	return msg
}

func enumMessage(where string, k *kind.Enum) string {
	want := make([]string, len(k.Want))
	for i, w := range k.Want {
		want[i] = describeValue(w)
	}
	switch where {
	case schemaActionType:
		return "unknown action type " + describeValue(k.Got) + "; use " + orList(want)
	case schemaMode:
		return describeValue(k.Got) + " is not a valid mode; use " + orList(want)
	}
	return describeValue(k.Got) + " is not a valid value; use " + orList(want)
}

func constMessage(where string, k *kind.Const) string {
	if where == schemaVersionProp {
		if _, ok := k.Got.(string); ok {
			return "schemaVersion must be the number " + describeValue(k.Want) + ", not a string"
		}
		return "schemaVersion must be " + describeValue(k.Want)
	}
	return "the value must be " + describeValue(k.Want)
}

// fallbackMessage is the library's English message, for error kinds the
// profile schema does not produce today.
func fallbackMessage(e *jsonschema.ValidationError) string {
	return e.ErrorKind.LocalizedString(message.NewPrinter(language.English))
}

// describeValue formats an instance or schema value for a message, cut to
// maxWord characters like quote.
func describeValue(v any) string {
	switch v := v.(type) {
	case string:
		return quote(v)
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	case nil:
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return shorten(fmt.Sprint(v))
	}
	return shorten(string(b)) // a number can have any number of digits
}

// withArticle returns a JSON type name as a noun phrase: "a string",
// "an object", "null".
func withArticle(t string) string {
	switch t {
	case "null":
		return "null"
	case "object", "array", "integer":
		return "an " + t
	}
	return "a " + t
}

// orList joins items as `a`, `a or b`, `a, b or c`.
func orList(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

func didYouMean(names []string) string {
	if len(names) == 0 {
		return ""
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = quote(n)
	}
	return "; did you mean " + orList(quoted) + "?"
}

// schemaDoc is the embedded schema scanned into nodes, for the property
// names at a schema location in document order.
var schemaDoc = sync.OnceValue(func() *node {
	n, _ := scan(schemaJSON)
	return n
})

// schemaAt returns the schema object at a SchemaURL fragment, or nil.
func schemaAt(where string) *node {
	root := schemaDoc()
	if root == nil {
		return nil
	}
	return root.lookupPointer(where)
}

// propertyNames returns the names in the properties of schema object sch.
func propertyNames(sch *node) []string {
	if sch == nil || sch.members["properties"] == nil {
		return nil
	}
	props := sch.members["properties"].order
	names := make([]string, len(props))
	for i, p := range props {
		names[i] = p.name
	}
	return names
}

// closed reports whether schema object sch forbids other properties.
func closed(sch *node) bool {
	return sch != nil && sch.members["additionalProperties"] != nil && sch.members["additionalProperties"].val == false
}

// suggest returns the names closest to s, ignoring case: those within an
// edit distance of 1, or 2 when s is longer than 4 bytes, and only the
// closest of them. It returns at most 3, in the order of names.
func suggest(s string, names []string) []string {
	if s == "" {
		return nil
	}
	ls := strings.ToLower(s)
	limit := 1
	if len(ls) > 4 {
		limit = 2
	}
	best := limit + 1
	var out []string
	for _, n := range names {
		d := distance(ls, strings.ToLower(n))
		switch {
		case d > limit: // too far to be a typo
		case d < best:
			best, out = d, append(out[:0], n)
		case d == best && len(out) < 3:
			out = append(out, n)
		}
	}
	return out
}

// distance is the optimal string alignment distance between a and b
// (Levenshtein plus adjacent transpositions), over bytes.
func distance(a, b string) int {
	if diff := len(a) - len(b); diff > 2 || diff < -2 {
		return 3 // further than any suggestion limit
	}
	// Three rolling rows: two back, previous, current.
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(b)]
}
