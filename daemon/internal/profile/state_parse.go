package profile

import (
	"bytes"
	_ "embed" // state.schema.json
	"errors"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:generate go run ../../tools/schemacopy -src ../../../schema/state.schema.json -dst state.schema.json

//go:embed state.schema.json
var stateSchemaJSON []byte

var compiledStateSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(stateSchemaJSON))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(noLoader{})
	if err := c.AddResource("urn:padmate:state.schema.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("urn:padmate:state.schema.json")
})

// ParseState validates persisted state; device identities are checked by the
// caller against the input backend's canonical DeviceKey representation.
func ParseState(data []byte) (State, Diagnostics) {
	root, scanErr := scan(data)
	if scanErr != nil {
		return State{}, place(data, Diagnostics{{Severity: SeverityError, Offset: scanErr.off, Message: scanErr.msg}})
	}
	schema, err := compiledStateSchema()
	if err != nil {
		return State{}, place(data, Diagnostics{internalError(err)})
	}
	if err := schema.Validate(root.val); err != nil {
		var ve *jsonschema.ValidationError
		if !errors.As(err, &ve) {
			return State{}, place(data, Diagnostics{internalError(err)})
		}
		var ds Diagnostics
		for _, e := range leaves(ve, nil) {
			n, ptr := (&flattener{root: root}).at(e.InstanceLocation)
			ds = append(ds, Diagnostic{Severity: SeverityError, Pointer: ptr, Offset: n.start, Message: fallbackMessage(e)})
		}
		return State{}, place(data, ds)
	}
	var state State
	if ds := decodeWire(data, root, &state); len(ds) > 0 {
		return State{}, place(data, ds)
	}
	var ds Diagnostics
	fail := func(ptr string) {
		n := root.lookupPointer(ptr)
		ds = append(ds, Diagnostic{Severity: SeverityError, Pointer: ptr, Offset: n.start, Message: "invalid profile library id"})
	}
	if state.LastProfile != "" && !ValidLibraryID(state.LastProfile) {
		fail("/lastProfile")
	}
	for key, device := range state.Devices {
		for id := range device.Profiles {
			if !ValidLibraryID(id) {
				fail(childPointer(childPointer("/devices", key)+"/profiles", id))
			}
		}
	}
	if ds.HasErrors() {
		return State{}, place(data, ds)
	}
	return state, nil
}
