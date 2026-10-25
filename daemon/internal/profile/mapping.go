package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlmapping"
)

// KeyGUID returns the GUID of a DeviceKey as config.json writes it: its first
// part, before "#N" and "/s:SERIAL" or "/p:PATH" (internal/input DeviceKey).
// It is "" when the key has none, and is not checked here.
func KeyGUID(key string) string {
	if i := strings.IndexAny(key, "#/"); i >= 0 {
		return key[:i]
	}
	return key
}

// checkMappings reports the custom gamepad mappings of the devices of c
// (IN-18, ADR-0072) that cannot work: one that is no mapping string, whose
// GUID is not the one of its device key, or that differs from the mapping of
// another key with the same GUID. SDL applies a mapping to every device with
// its GUID, so keys with one GUID must agree; the wizard writes them together.
func checkMappings(devices map[string]DeviceSettings, fail func(pointer, msg string)) {
	keys := make([]string, 0, len(devices))
	for key, d := range devices {
		if d.Mapping != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	first := map[string]string{} // GUID -> the first key that has a mapping
	for _, key := range keys {
		ptr := childPointer(childPointer("/devices", key), "mapping")
		m, err := sdlmapping.Parse(devices[key].Mapping)
		if err == nil {
			err = m.Validate()
		}
		if err != nil {
			fail(ptr, "expected an SDL gamepad mapping string: "+err.Error())
			continue
		}
		guid := strings.ToLower(KeyGUID(key))
		if guid != m.GUID {
			fail(ptr, "the mapping is for GUID "+m.GUID+", not for the device key's "+KeyGUID(key))
			continue
		}
		other, ok := first[guid]
		if !ok {
			first[guid] = key
			continue
		}
		om, _ := sdlmapping.Parse(devices[other].Mapping)
		if !sdlmapping.Equivalent(om, m) {
			fail(ptr, "SDL applies a mapping to every device with this GUID: it must be the same as the mapping of "+other)
		}
	}
}

// DeviceMappings returns the custom gamepad mappings of devices, one for
// each GUID, as canonical strings in the order of their GUIDs. Mappings that
// do not parse are left out (ParseConfig refuses them).
func DeviceMappings(devices map[string]DeviceSettings) []string {
	keys := make([]string, 0, len(devices))
	for key, d := range devices {
		if d.Mapping != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	byGUID := map[string]string{}
	for _, key := range keys {
		m, err := sdlmapping.Parse(devices[key].Mapping)
		if err != nil {
			continue
		}
		if _, ok := byGUID[m.GUID]; !ok {
			byGUID[m.GUID] = m.String()
		}
	}
	out := make([]string, 0, len(byGUID))
	for _, g := range sortedKeys(byGUID) {
		out = append(out, byGUID[g])
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// SetDeviceMappings returns data, a config.json, with the mapping of each
// device key of mappings set to its value, or taken out when the value is "".
// Every other member stays with its bytes and its place; the result is
// indented as the UI writes the file, without a BOM. A device that is left
// with no member by taking its mapping out is removed; keys that are new are
// added to the end of devices, in order. The result is not checked: the
// caller parses it (profilelib.ValidateConfig).
func SetDeviceMappings(data []byte, mappings map[string]string) ([]byte, error) {
	root, serr := scan(data)
	if serr != nil {
		return nil, fmt.Errorf("config.json: %s", serr.msg)
	}
	if _, ok := root.val.(map[string]any); !ok {
		return nil, errors.New("config.json is not a JSON object")
	}
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	next := func() {
		if !first {
			b.WriteByte(',')
		}
		first = false
	}
	done := false
	for _, m := range root.order {
		next()
		b.Write(data[m.key:stringEnd(data, m.key)])
		b.WriteByte(':')
		if m.name == "devices" {
			writeDevices(&b, data, m, mappings)
			done = true
			continue
		}
		b.Write(data[m.start:m.end])
	}
	if !done {
		want := false
		for _, v := range mappings {
			want = want || v != ""
		}
		if want {
			next()
			b.WriteString(`"devices":`)
			writeDevices(&b, data, nil, mappings)
		}
	}
	b.WriteByte('}')
	return indentConfig(b.Bytes()), nil
}

// writeDevices writes the devices member: the object n (nil for none) with
// the mappings changed.
func writeDevices(b *bytes.Buffer, data []byte, n *node, mappings map[string]string) {
	isObject := false
	if n != nil {
		_, isObject = n.val.(map[string]any)
	}
	if n != nil && !isObject {
		b.Write(data[n.start:n.end]) // not ours to repair
		return
	}
	b.WriteByte('{')
	first := true
	next := func() {
		if !first {
			b.WriteByte(',')
		}
		first = false
	}
	seen := map[string]bool{}
	if n != nil {
		for _, d := range n.order {
			seen[d.name] = true
			mapping, set := mappings[d.name]
			if !set {
				next()
				b.Write(data[d.key:stringEnd(data, d.key)])
				b.WriteByte(':')
				b.Write(data[d.start:d.end])
				continue
			}
			var body bytes.Buffer
			if !writeDevice(&body, data, d, mapping) {
				continue
			}
			next()
			b.Write(data[d.key:stringEnd(data, d.key)])
			b.WriteByte(':')
			b.Write(body.Bytes())
		}
	}
	for _, key := range sortedKeys(mappings) {
		if seen[key] || mappings[key] == "" {
			continue
		}
		next()
		b.Write(jsonString(key))
		b.WriteString(`:{"mapping":`)
		b.Write(jsonString(mappings[key]))
		b.WriteByte('}')
	}
	b.WriteByte('}')
}

// writeDevice writes the device d with its mapping replaced (taken out for
// ""). It reports false when nothing is left and d is to go.
func writeDevice(b *bytes.Buffer, data []byte, d *node, mapping string) bool {
	if _, ok := d.val.(map[string]any); !ok {
		b.Write(data[d.start:d.end])
		return true
	}
	b.WriteByte('{')
	n := 0
	for _, m := range d.order {
		if m.name == "mapping" {
			continue
		}
		if n > 0 {
			b.WriteByte(',')
		}
		n++
		b.Write(data[m.key:stringEnd(data, m.key)])
		b.WriteByte(':')
		b.Write(data[m.start:m.end])
	}
	if mapping != "" {
		if n > 0 {
			b.WriteByte(',')
		}
		n++
		b.WriteString(`"mapping":`)
		b.Write(jsonString(mapping))
	}
	b.WriteByte('}')
	return n > 0 || len(d.order) == 0
}

// jsonString encodes s as a JSON string without escaping HTML.
func jsonString(s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimRight(b.Bytes(), "\n")
}
