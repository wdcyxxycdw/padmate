package compile

import (
	_ "embed" // for goja_features.json
	"encoding/json"
	"fmt"
)

//go:embed goja_features.json
var featuresJSON []byte

// supported is esbuild's BuildOptions.Supported table for goja, parsed once
// from goja_features.json.
var supported = mustParseFeatures(featuresJSON)

func mustParseFeatures(data []byte) map[string]bool {
	var f struct {
		Supported map[string]bool `json:"supported"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		panic(fmt.Sprintf("compile: goja_features.json: %v", err))
	}
	if len(f.Supported) == 0 {
		panic("compile: goja_features.json has no supported table")
	}
	return f.Supported
}
