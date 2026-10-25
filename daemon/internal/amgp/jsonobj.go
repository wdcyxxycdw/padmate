package amgp

import (
	"bytes"
	"encoding/json"
	"math"
)

// obj is a JSON object that keeps the order its members were set in, so
// that the imported profile reads like one written by hand: the name first,
// a binding's label before its actions. Nothing here is untrusted text
// except the values, which encoding/json escapes.
type obj struct {
	keys []string
	vals map[string]any
}

func newObj() *obj { return &obj{vals: map[string]any{}} }

// set sets a member, in the position of its first set.
func (o *obj) set(key string, val any) *obj {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = val
	return o
}

func (o *obj) empty() bool { return len(o.keys) == 0 }

// MarshalJSON writes the members in order.
func (o *obj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := encode(k)
		if err != nil {
			return nil, err
		}
		val, err := encode(o.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(val)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// encode marshals v without escaping <, > and &, which a label may hold.
func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// render writes o as indented text with a final newline.
func render(o *obj) ([]byte, error) {
	compact, err := encode(o)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// r4 rounds to four decimals, so that 8000/32767 is written 0.2441.
func r4(v float64) float64 { return math.Round(v*1e4) / 1e4 }
