package profile

import "fmt"

// DecodeStrict parses data with the strict JSON scanner that profile.json and
// config.json use (RFC 8259 without trailing commas, comments, duplicate
// keys or invalid UTF-8; a leading BOM is skipped). It returns the document
// as map[string]any, []any, string, json.Number, bool or nil. The error says
// where the first problem is as "line:column: message". Files with their own
// schema (integrations/<id>.json) use it so that they are as strict as a
// profile.
func DecodeStrict(data []byte) (any, error) {
	root, serr := scan(data)
	if serr != nil {
		line, col := position(data, serr.off)
		return nil, fmt.Errorf("%d:%d: %s", line, col, serr.msg)
	}
	return root.val, nil
}
