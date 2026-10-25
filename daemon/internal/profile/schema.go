package profile

import (
	"bytes"
	_ "embed" // for profile.schema.json
	"errors"
	"slices"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed profile.schema.json
var schemaJSON []byte

// Schema returns the profile JSON Schema (draft 2020-12) that the package
// validates against. The caller owns the slice.
func Schema() []byte { return slices.Clone(schemaJSON) }

// schemaURL identifies the embedded schema to the compiler. An absolute URL
// keeps the compiler off the file system.
const schemaURL = "urn:padmate:profile.schema.json"

// compiledSchema compiles the embedded schema on first use. It never fails
// for a schema that passes TestSchemaCompiles. The compiled schema is
// immutable and safe for concurrent Validate calls.
var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(noLoader{})
	if err := c.AddResource(schemaURL, doc); err != nil {
		return nil, err
	}
	return c.Compile(schemaURL)
})

// noLoader refuses every external reference: the schema must be
// self-contained. The draft 2020-12 metaschema is built into the compiler
// and never reaches the loader.
type noLoader struct{}

func (noLoader) Load(url string) (any, error) {
	return nil, errors.New("profile schema refers to " + url + "; it must be self-contained")
}
