package script

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// ParamInfo is a detached description for editors and action metadata.
type ParamInfo struct {
	Kind      string   `json:"kind"`
	Label     string   `json:"label,omitempty"`
	Default   any      `json:"default"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	Step      *float64 `json:"step,omitempty"`
	MaxLength *int     `json:"maxLength,omitempty"`
	Options   []string `json:"options,omitempty"`
	// LayerOptions is nil when profile layer metadata is unavailable.
	LayerOptions []string `json:"layerOptions,omitempty"`
}

func (p ParamInfo) clone() ParamInfo {
	if p.Min != nil {
		v := *p.Min
		p.Min = &v
	}
	if p.Max != nil {
		v := *p.Max
		p.Max = &v
	}
	if p.Step != nil {
		v := *p.Step
		p.Step = &v
	}
	if p.MaxLength != nil {
		v := *p.MaxLength
		p.MaxLength = &v
	}
	p.Options = slices.Clone(p.Options)
	p.LayerOptions = slices.Clone(p.LayerOptions)
	return p
}

func (h *Host) parameterAPI() *goja.Object {
	o := h.VM.NewObject()
	for _, kind := range []string{"number", "boolean", "choice", "text", "key", "control", "layer"} {
		setFunction(h.VM, o, kind, 1, func(call goja.FunctionCall) goja.Value {
			p, err := h.parseParam(kind, call.Argument(0))
			if err != nil {
				panic(h.VM.NewTypeError("param.%s: %s", kind, err))
			}
			descriptor := h.VM.NewObject()
			h.paramSpecs[descriptor] = p
			return descriptor
		})
	}
	return o
}

func absent(v goja.Value) bool { return v == nil || goja.IsUndefined(v) }

func jsPrimitive(v goja.Value) any {
	if absent(v) || goja.IsNull(v) {
		return nil
	}
	if _, object := v.(*goja.Object); object {
		return nil
	}
	return v.Export()
}

func finiteNumber(v any) (float64, bool) {
	var n float64
	switch v := v.(type) {
	case float64:
		n = v
	case int64:
		n = float64(v)
	case int:
		n = float64(v)
	case json.Number:
		var err error
		n, err = v.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0)
}

func (h *Host) parseParam(kind string, value goja.Value) (ParamInfo, error) {
	p := ParamInfo{Kind: kind}
	if kind == "layer" {
		p.LayerOptions = h.LayerNames()
	}
	o, ok := value.(*goja.Object)
	if !ok || o.ClassName() != "Object" {
		return p, fmt.Errorf("expected an options object")
	}
	if label := o.Get("label"); !absent(label) {
		var valid bool
		p.Label, valid = jsPrimitive(label).(string)
		if !valid {
			return p, fmt.Errorf("label must be a string")
		}
	}
	for _, field := range []struct {
		name string
		dst  **float64
	}{
		{"min", &p.Min}, {"max", &p.Max}, {"step", &p.Step},
	} {
		if v := o.Get(field.name); !absent(v) {
			n, valid := finiteNumber(jsPrimitive(v))
			if kind != "number" || !valid {
				return p, fmt.Errorf("%s must be a finite number", field.name)
			}
			*field.dst = &n
		}
	}
	if p.Min != nil && p.Max != nil && *p.Min > *p.Max {
		return p, fmt.Errorf("min must not exceed max")
	}
	if p.Step != nil && *p.Step <= 0 {
		return p, fmt.Errorf("step must be positive")
	}
	if v := o.Get("maxLength"); !absent(v) {
		n, valid := finiteNumber(jsPrimitive(v))
		if kind != "text" || !valid || n < 0 || n > 1_000_000 || n != math.Trunc(n) {
			return p, fmt.Errorf("maxLength must be an integer between 0 and 1000000")
		}
		i := int(n)
		p.MaxLength = &i
	}
	if kind == "choice" {
		options, valid := o.Get("options").(*goja.Object)
		if !valid || options.ClassName() != "Array" {
			return p, fmt.Errorf("options must be a non-empty string array")
		}
		n := options.Get("length").ToInteger()
		if n < 1 || n > 4096 {
			return p, fmt.Errorf("options must contain 1..4096 choices")
		}
		for i := int64(0); i < n; i++ {
			v, valid := jsPrimitive(options.Get(fmt.Sprint(i))).(string)
			if !valid || slices.Contains(p.Options, v) {
				return p, fmt.Errorf("options must contain distinct strings")
			}
			p.Options = append(p.Options, v)
		}
	}
	var err error
	p.Default, err = p.normalize(jsPrimitive(o.Get("default")))
	if err != nil {
		return p, fmt.Errorf("default: %w", err)
	}
	return p, nil
}

func (p ParamInfo) normalize(v any) (any, error) {
	switch p.Kind {
	case "number":
		n, ok := finiteNumber(v)
		if !ok {
			return nil, fmt.Errorf("expected a finite number")
		}
		if p.Min != nil {
			n = math.Max(n, *p.Min)
		}
		if p.Max != nil {
			n = math.Min(n, *p.Max)
		}
		if p.Step != nil {
			base := 0.0
			if p.Min != nil {
				base = *p.Min
			}
			rounded := base + math.Round((n-base) / *p.Step)**p.Step
			if !math.IsInf(rounded, 0) && !math.IsNaN(rounded) {
				n = rounded
			}
			if p.Min != nil {
				n = math.Max(n, *p.Min)
			}
			if p.Max != nil {
				n = math.Min(n, *p.Max)
			}
		}
		return n, nil
	case "boolean":
		if b, ok := v.(bool); ok {
			return b, nil
		}
		return nil, fmt.Errorf("expected a boolean")
	default:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("expected a string")
		}
		switch p.Kind {
		case "text":
			if p.MaxLength != nil {
				runes := []rune(s)
				if len(runes) > *p.MaxLength {
					s = string(runes[:*p.MaxLength])
				}
			}
		case "choice":
			if !slices.Contains(p.Options, s) {
				return nil, fmt.Errorf("unknown choice %q", s)
			}
		case "key":
			chord, err := keys.Parse(s)
			if err != nil {
				return nil, err
			}
			s = chord.String()
		case "control":
			c, valid := control.Parse(s)
			if !valid {
				return nil, fmt.Errorf("unknown control %q", s)
			}
			s = c.String()
		case "layer":
			if strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("layer must not be blank")
			}
			if p.LayerOptions != nil && !slices.Contains(p.LayerOptions, s) {
				return nil, fmt.Errorf("unknown layer %q", s)
			}
		}
		return s, nil
	}
}

func (h *Host) readParamSchema(v goja.Value) (map[string]ParamInfo, error) {
	out := make(map[string]ParamInfo)
	if absent(v) {
		return out, nil
	}
	o, ok := v.(*goja.Object)
	if !ok || o.ClassName() != "Object" {
		return nil, fmt.Errorf("expected a parameter schema object")
	}
	for _, name := range o.Keys() {
		descriptor, ok := o.Get(name).(*goja.Object)
		if !ok {
			return nil, fmt.Errorf("%s: expected a param descriptor", name)
		}
		p, ok := h.paramSpecs[descriptor]
		if !ok {
			return nil, fmt.Errorf("%s: expected a param descriptor", name)
		}
		out[name] = p
	}
	return out, nil
}

func normalizeParams(schema map[string]ParamInfo, raw map[string]any) (map[string]any, error) {
	for name := range raw {
		if _, ok := schema[name]; !ok {
			return nil, fmt.Errorf("unknown parameter %q", name)
		}
	}
	result := make(map[string]any, len(schema))
	for name, spec := range schema {
		v, supplied := raw[name]
		if !supplied {
			v = spec.Default
		}
		normalized, err := spec.normalize(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		result[name] = normalized
	}
	return result, nil
}

func (h *Host) readonlyValues(values map[string]any) *goja.Object {
	o := h.VM.NewObject()
	_ = o.SetPrototype(nil)
	for name, value := range values {
		_ = o.DefineDataProperty(name, h.VM.ToValue(value), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	}
	return o
}

func cloneJSONValues(raw map[string]any) (map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var cloned map[string]any
	err = json.Unmarshal(data, &cloned)
	return cloned, err
}
