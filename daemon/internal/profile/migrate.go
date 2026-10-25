package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// CurrentSchema is the newest profile version understood by this build. The
// format was frozen as version 1 in M14 (PRO-01): from then on a profile that
// is valid stays valid, new optional fields do not change the version, and a
// change that could break an existing profile adds a migration and bumps it.
const CurrentSchema = 1

type migration func([]byte) ([]byte, error)

// migrations[n] upgrades n to n+1. Version 0 is the format of the previews
// before the freeze. New steps must retain the fields they do not change and
// must never write files.
var migrations = []migration{migrateV0}

// migrateV0 upgrades version 0 to version 1. The two formats are identical,
// so only the value of schemaVersion changes. The new number is padded with
// spaces to the length of the old token ("0.0", "-0", "0e0" are all version
// 0), which keeps every other byte and every offset of the document in place:
// Parse reports diagnostics at positions of the text the user sees.
func migrateV0(data []byte) ([]byte, error) {
	root, serr := scan(data)
	if serr != nil {
		return nil, errors.New(serr.msg)
	}
	n := root.members["schemaVersion"]
	if n == nil || n.end <= n.start {
		return nil, errors.New("no schemaVersion to change")
	}
	out := bytes.Clone(data)
	for i := n.start; i < n.end; i++ {
		out[i] = ' '
	}
	out[n.start] = '1'
	return out, nil
}

// Migrate upgrades a document in memory, without modifying its source: no
// file is ever rewritten because it is old. A current document retains its
// exact bytes (including whitespace and BOM). Future versions, malformed JSON
// and incomplete chains fail without returning partial data. Parse migrates
// older documents itself and still validates the resulting schema and
// semantics; call Migrate when the upgraded bytes are needed.
func Migrate(data []byte) ([]byte, Diagnostics) {
	return migrate(data, migrations)
}

func migrate(data []byte, steps []migration) ([]byte, Diagnostics) {
	version, ds := documentVersion(data)
	if ds.HasErrors() {
		return nil, ds
	}
	if version > len(steps) {
		return nil, versionError(data, fmt.Sprintf("schemaVersion %d is newer than supported version %d; upgrade padmate", version, len(steps)))
	}
	result := bytes.Clone(data)
	for version < len(steps) {
		step := steps[version]
		if step == nil {
			return nil, versionError(data, fmt.Sprintf("missing migration from schemaVersion %d", version))
		}
		next, err := step(result)
		if err != nil {
			return nil, versionError(data, fmt.Sprintf("migration from schemaVersion %d: %v", version, err))
		}
		v, ds := documentVersion(next)
		if ds.HasErrors() || v != version+1 {
			return nil, versionError(data, fmt.Sprintf("migration from schemaVersion %d did not produce version %d", version, version+1))
		}
		result, version = next, v
	}
	return result, nil
}

func documentVersion(data []byte) (int, Diagnostics) {
	root, err := scan(data)
	if err != nil {
		return 0, place(data, Diagnostics{{Severity: SeverityError, Offset: err.off, Message: err.msg}})
	}
	if v, ok := rootVersion(root); ok {
		return v, nil
	}
	return 0, versionError(data, "schemaVersion must be a non-negative integer")
}

// rootVersion returns the schemaVersion of the scanned document root, when
// it is a non-negative integer (1.0 and 1e0 are 1).
func rootVersion(root *node) (int, bool) {
	n := root.members["schemaVersion"]
	if n == nil {
		return 0, false
	}
	num, ok := n.val.(json.Number)
	if !ok {
		return 0, false
	}
	v, err := num.Float64()
	mantissa := strings.FieldsFunc(num.String(), func(c rune) bool { return c == 'e' || c == 'E' })[0]
	underflow := v == 0 && strings.ContainsAny(mantissa, "123456789")
	if err != nil || underflow || v < 0 || v > math.MaxInt32 || v != math.Trunc(v) {
		return 0, false
	}
	return int(v), true
}

func versionError(data []byte, message string) Diagnostics {
	off := 0
	if root, err := scan(data); err == nil {
		if n := root.members["schemaVersion"]; n != nil {
			off = n.start
		}
	}
	return place(data, Diagnostics{{Severity: SeverityError, Pointer: "/schemaVersion", Offset: off, Message: message}})
}
