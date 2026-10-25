package profile

import "encoding/json"

//go:generate go run ../../tools/codegen -root ../../..

// Layer decoding keeps metadata separate from canonical and alias control
// names. The schema-derived representation is in wire_generated.go.
//
// This is the one wire type with its own decoder. encoding/json reports an
// error inside it with a position relative to the layer, so the walk that
// finds the numbers no int takes (intProblems, wholenumber.go) knows its shape.
func (w *wireLayer) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["color"]; ok {
		var color string
		if err := json.Unmarshal(v, &color); err != nil {
			return err
		}
		w.Color = &color
		delete(raw, "color")
	}
	w.Bindings = make(map[string]*wireBinding, len(raw))
	for name, v := range raw {
		var b *wireBinding
		if err := json.Unmarshal(v, &b); err != nil {
			return err
		}
		w.Bindings[name] = b
	}
	return nil
}
