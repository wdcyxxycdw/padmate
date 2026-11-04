package profile

import (
	"bytes"
	"errors"
	"fmt"
	"slices"

	"github.com/wdcyxxycdw/padmate/daemon/internal/padfamily"
)

// jsonPatch sets the value at path (a list of member names, from the root
// object) to value, a JSON text, or removes the member when value is nil.
type jsonPatch struct {
	path  []string
	value []byte
}

// patchTree is the patches as a tree of member names.
type patchTree struct {
	keys     []string // the children in the order they were first patched
	children map[string]*patchTree
	set      bool
	remove   bool
	value    []byte
}

func (t *patchTree) child(name string) *patchTree {
	c := t.children[name]
	if c == nil {
		if t.children == nil {
			t.children = map[string]*patchTree{}
		}
		c = &patchTree{}
		t.children[name] = c
		t.keys = append(t.keys, name)
	}
	return c
}

// patchConfigText returns data, a config.json, with the patches applied.
// Every other member stays with its bytes and its place, as in
// SetDeviceMappings; a member that is new goes to the end of its object, in
// the order of the patches; an object that a patch leaves with no member is
// taken out, unless it was empty before. The result is indented as the UI
// writes the file, without a BOM. It is not checked: the caller parses it
// (profilelib.ValidateConfig). A patch below a member that is no object is an
// error.
func patchConfigText(data []byte, patches []jsonPatch) ([]byte, error) {
	root, serr := scan(data)
	if serr != nil {
		return nil, fmt.Errorf("config.json: %s", serr.msg)
	}
	if _, ok := root.val.(map[string]any); !ok {
		return nil, errors.New("config.json is not a JSON object")
	}
	tree := &patchTree{}
	for _, p := range patches {
		t := tree
		for _, name := range p.path {
			t = t.child(name)
		}
		t.set, t.remove, t.value = p.value != nil, p.value == nil, p.value
	}
	var b bytes.Buffer
	kept, err := patchedValue(&b, data, root, tree)
	if err != nil {
		return nil, err
	}
	if !kept { // not reached: schemaVersion stays
		b.WriteString("{}")
	}
	return indentConfig(b.Bytes()), nil
}

// patchedValue writes the value n (nil for none) with the patches t. It
// reports kept false, writing nothing, when the value is to go.
func patchedValue(b *bytes.Buffer, data []byte, n *node, t *patchTree) (kept bool, err error) {
	switch {
	case t.remove:
		return false, nil
	case t.set:
		b.Write(t.value)
		return true, nil
	case len(t.keys) == 0:
		if n == nil {
			return false, nil
		}
		b.Write(data[n.start:n.end])
		return true, nil
	}
	if n != nil {
		if _, ok := n.val.(map[string]any); !ok {
			return false, errors.New("config.json: a member that is no object cannot take settings")
		}
	}
	var body bytes.Buffer
	count := 0
	member := func(key []byte, write func(*bytes.Buffer) (bool, error)) error {
		var value bytes.Buffer
		kept, err := write(&value)
		if err != nil || !kept {
			return err
		}
		if count > 0 {
			body.WriteByte(',')
		}
		count++
		body.Write(key)
		body.WriteByte(':')
		body.Write(value.Bytes())
		return nil
	}
	if n != nil {
		for _, m := range n.order {
			c := t.children[m.name]
			err := member(data[m.key:stringEnd(data, m.key)], func(v *bytes.Buffer) (bool, error) {
				if c == nil {
					v.Write(data[m.start:m.end])
					return true, nil
				}
				return patchedValue(v, data, m, c)
			})
			if err != nil {
				return false, err
			}
		}
	}
	for _, name := range t.keys {
		if n != nil && n.members[name] != nil {
			continue
		}
		err := member(jsonString(name), func(v *bytes.Buffer) (bool, error) {
			return patchedValue(v, data, nil, t.children[name])
		})
		if err != nil {
			return false, err
		}
	}
	if count == 0 && (n == nil || len(n.order) > 0) {
		return false, nil
	}
	b.WriteByte('{')
	b.Write(body.Bytes())
	b.WriteByte('}')
	return true, nil
}

// PadChange is a change to the settings of one controller, for
// SetPadSettings. A member whose Set flag is false stays as it is.
type PadChange struct {
	// SetFunctionKey sets devices.<key>.functionKey to FunctionKey, a
	// canonical control name, or takes it out when FunctionKey is "".
	SetFunctionKey bool
	FunctionKey    string
	// SetFeedback sets devices.<key>.feedback to Feedback, or takes it out
	// when Feedback is nil or sets no switch.
	SetFeedback bool
	Feedback    *FeedbackOverrides
	// SetPanic sets devices.<key>.panic to Panic, or takes it out when
	// Panic is nil.
	SetPanic bool
	Panic    *PanicCombo
}

// IsZero reports whether the change changes nothing.
func (c PadChange) IsZero() bool { return !c.SetFunctionKey && !c.SetFeedback && !c.SetPanic }

func (c PadChange) patches(key string) []jsonPatch {
	base := []string{"devices", key}
	at := func(member string) []string { return append(slices.Clone(base), member) }
	var out []jsonPatch
	if c.SetFunctionKey {
		p := jsonPatch{path: at("functionKey")}
		if c.FunctionKey != "" {
			p.value = jsonString(c.FunctionKey)
		}
		out = append(out, p)
	}
	if c.SetFeedback {
		out = append(out, jsonPatch{path: at("feedback"), value: feedbackJSON(c.Feedback)})
	}
	if c.SetPanic {
		p := jsonPatch{path: at("panic")}
		if c.Panic != nil {
			p.value = panicJSON(*c.Panic)
		}
		out = append(out, p)
	}
	return out
}

// feedbackJSON is the JSON of the switches f sets, in the order of the
// schema's description, or nil when it sets none.
func feedbackJSON(f *FeedbackOverrides) []byte {
	if f == nil {
		return nil
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for _, m := range []struct {
		name string
		v    *bool
	}{{"rumble", f.Rumble}, {"led", f.Led}, {"pause", f.Pause}, {"lowBattery", f.LowBattery}} {
		if m.v == nil {
			continue
		}
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		b.Write(jsonString(m.name))
		if *m.v {
			b.WriteString(":true")
		} else {
			b.WriteString(":false")
		}
	}
	if b.Len() == 1 {
		return nil
	}
	b.WriteByte('}')
	return b.Bytes()
}

func panicJSON(p PanicCombo) []byte {
	var b bytes.Buffer
	b.WriteString(`{"buttons":[`)
	for i, name := range p.Buttons {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(jsonString(name))
	}
	fmt.Fprintf(&b, `],"holdMs":%d}`, p.HoldMs)
	return b.Bytes()
}

// SetPadSettings returns data, a config.json, with the change made to the
// entry of devices for the controller with DeviceKey key: the entry is made
// if it is not there, and taken out when the change leaves it with no member.
// Everything else stays as in patchConfigText.
func SetPadSettings(data []byte, key string, change PadChange) ([]byte, error) {
	return patchConfigText(data, change.patches(key))
}

// SetIntegrationDataDir returns data, a config.json, with
// integrations.<id>.dataDir set to dir, or taken out when dir is "" (the
// integration is then found automatically again). Everything else stays as in
// patchConfigText; an integrations entry or object the change leaves empty
// goes with it.
func SetIntegrationDataDir(data []byte, id, dir string) ([]byte, error) {
	p := jsonPatch{path: []string{"integrations", id, "dataDir"}}
	if dir != "" {
		p.value = jsonString(dir)
	}
	return patchConfigText(data, []jsonPatch{p})
}

// PadRef names a controller for MigratePadSettings.
type PadRef struct {
	// Key is the controller's canonical DeviceKey.
	Key string
	// Family is the family of the controller as far as it is known;
	// Unknown is taken from the key.
	Family padfamily.Family
}

// MigratePadSettings returns data, a config.json, with the retired top-level
// functionKey, feedback and safety.panic (ADR-0017) folded into the
// controllers and taken out: for each controller of pads, the entry of
// devices gets what the retired members give it where that differs from its
// family's defaults (Config.Materialized), so the controller behaves as before.
// changed is false, and data is returned as it is, when the file has none of
// the retired members. A controller that is in neither pads nor devices
// takes the defaults of its family from now on. The result is not
// checked; the caller parses it. An error is returned for a file that does not
// parse.
func MigratePadSettings(data []byte, pads []PadRef) (out []byte, changed bool, err error) {
	c, ds := ParseConfig(data)
	if err := ds.Err(); err != nil {
		return nil, false, err
	}
	if !c.Legacy.Any() {
		return data, false, nil
	}
	keys := map[string]padfamily.Family{}
	for key := range c.Devices {
		keys[key] = padfamily.Unknown
	}
	for _, p := range pads {
		keys[p.Key] = p.Family
	}
	var patches []jsonPatch
	for _, key := range sortedKeys(keys) {
		before := c.Devices[key]
		after := c.Materialized(key, keys[key])
		change := PadChange{}
		if after.FunctionKey != before.FunctionKey {
			change.SetFunctionKey, change.FunctionKey = true, after.FunctionKey
		}
		if !sameFeedback(before.Feedback, after.Feedback) {
			change.SetFeedback, change.Feedback = true, after.Feedback
		}
		if (before.Panic == nil) != (after.Panic == nil) {
			change.SetPanic, change.Panic = true, after.Panic
		}
		patches = append(patches, change.patches(key)...)
	}
	for _, member := range [][]string{{"functionKey"}, {"feedback"}, {"safety", "panic"}} {
		patches = append(patches, jsonPatch{path: member})
	}
	out, err = patchConfigText(data, patches)
	return out, err == nil, err
}

func sameFeedback(a, b *FeedbackOverrides) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	same := func(x, y *bool) bool { return x == nil && y == nil || x != nil && y != nil && *x == *y }
	return same(a.Rumble, b.Rumble) && same(a.Led, b.Led) && same(a.Pause, b.Pause) && same(a.LowBattery, b.LowBattery)
}
