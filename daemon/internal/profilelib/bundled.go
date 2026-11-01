package profilelib

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// The profiles padmate ships with (ADR-0008), each with the application it is
// made for. A release daemon offers each to the library once (EnsureBundled).
var bundledProfiles = []struct {
	ID, Exe string
}{
	{"csp", "CLIPStudioPaint.exe"},
}

//go:embed bundled/*/profile.json
var bundledFiles embed.FS

// BundledFile is the file of the data folder that remembers which shipped
// profiles have been offered to the library, so that one the person deleted,
// and its rule, never come back.
const BundledFile = "bundled.json"

// bundledRecord is what BundledFile holds.
type bundledRecord struct {
	Offered []string `json:"offered"`
}

// BundledText returns the profile.json padmate ships for id.
func BundledText(id string) ([]byte, error) {
	return bundledFiles.ReadFile(path.Join("bundled", id, "profile.json"))
}

// BundledIDs lists the IDs of the shipped profiles, in the order they are offered.
func BundledIDs() []string {
	ids := make([]string, 0, len(bundledProfiles))
	for _, b := range bundledProfiles {
		ids = append(ids, b.ID)
	}
	return ids
}

// BundledResult says what EnsureBundled did: the profiles it added to the
// library, and the applications whose rule it added to config.json.
type BundledResult struct {
	Added, Rules []string
}

// EnsureBundled offers each shipped profile the library has not been offered
// yet (BundledFile). A profile is added only when the library has no profile
// with its ID or its name: one of the person's own is never touched. When it is
// added, a rule that selects it for its application is appended to
// config.json, unless config.json has a rule for that application already.
// Either way the profile counts as offered, and is not offered again, so a
// profile or a rule the person deletes stays deleted. A BundledFile that
// cannot be read or does not parse offers nothing: it is an error, and
// nothing is added on a guess.
func (l *Library) EnsureBundled() (BundledResult, error) {
	var result BundledResult
	record, err := l.readBundled()
	if err != nil {
		return result, err
	}
	changed := false
	var errs []error
	for _, b := range bundledProfiles {
		if slices.Contains(record.Offered, b.ID) {
			continue
		}
		added, err := l.addBundled(b.ID)
		if err != nil {
			// Offered again at the next start.
			errs = append(errs, err)
			continue
		}
		record.Offered, changed = append(record.Offered, b.ID), true
		if !added {
			continue
		}
		result.Added = append(result.Added, b.ID)
		if b.Exe == "" {
			continue
		}
		ruled, err := l.addRule(b.Exe, b.ID)
		if err != nil {
			errs = append(errs, err)
		} else if ruled {
			result.Rules = append(result.Rules, b.Exe)
		}
	}
	if changed {
		data, err := json.Marshal(record)
		if err == nil {
			err = l.writeData(BundledFile, append(data, '\n'))
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("profile library: %s: %w", BundledFile, err))
		}
	}
	return result, errors.Join(errs...)
}

func (l *Library) readBundled() (bundledRecord, error) {
	var record bundledRecord
	data, err := l.readData(BundledFile)
	if errors.Is(err, fs.ErrNotExist) {
		return record, nil
	}
	if err != nil {
		return record, fmt.Errorf("profile library: %s: %w", BundledFile, err)
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return record, fmt.Errorf("profile library: %s: %w", BundledFile, err)
	}
	return record, nil
}

// addBundled writes the shipped profile id unless the library has a profile
// of that ID or name, and reports whether it did.
func (l *Library) addBundled(id string) (bool, error) {
	data, err := BundledText(id)
	if err != nil {
		return false, err
	}
	doc, ds := l.Parse(id, data)
	if err := ds.Err(); err != nil {
		return false, fmt.Errorf("profile library: shipped profile %s: %w", id, err)
	}
	if _, err := l.Read(id); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	var taken *NameTakenError
	if err := l.checkName(doc.Name, id); errors.As(err, &taken) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	// The revision of a missing file: a profile made meanwhile is kept.
	if _, err := l.Put(id, "", data); err != nil {
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// addRule appends {"exe":exe,"profile":id} to config.json's rules unless a
// rule of config.json is for the application exe already, by its file name or
// a path that ends in it, and reports whether it did. config.json keeps its
// fields in their order; it is written indented, as the window writes it.
func (l *Library) addRule(exe, id string) (bool, error) {
	data, revision, err := l.ConfigText()
	if err != nil {
		return false, err
	}
	c, err := ValidateConfig(data)
	if err != nil {
		// A config.json with errors is the person's to repair.
		return false, err
	}
	for _, rule := range c.Rules {
		if strings.EqualFold(path.Base(strings.ReplaceAll(rule.Exe, `\`, "/")), exe) {
			return false, nil
		}
	}
	updated, err := appendRule(data, exe, id)
	if err != nil {
		return false, err
	}
	if _, err := l.PutConfig(revision, updated); err != nil {
		return false, err
	}
	return true, nil
}

// appendRule returns the JSON object data with a rule for exe and id at the
// end of its rules, which it gets when it has none, and every other field as
// it was, in its place.
func appendRule(data []byte, exe, id string) ([]byte, error) {
	type field struct {
		key   string
		value json.RawMessage
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("profile library: config.json is not an object")
	}
	var fields []field
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		fields = append(fields, field{key, value})
	}
	rule, err := json.Marshal(struct {
		Exe     string `json:"exe"`
		Profile string `json:"profile"`
	}{exe, id})
	if err != nil {
		return nil, err
	}
	found := false
	for i, f := range fields {
		if f.key != "rules" {
			continue
		}
		var rules []json.RawMessage
		if err := json.Unmarshal(f.value, &rules); err != nil {
			return nil, err
		}
		if fields[i].value, err = json.Marshal(append(rules, rule)); err != nil {
			return nil, err
		}
		found = true
	}
	if !found {
		rules, _ := json.Marshal([]json.RawMessage{rule})
		fields = append(fields, field{"rules", rules})
	}
	var compact bytes.Buffer
	compact.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			compact.WriteByte(',')
		}
		key, _ := json.Marshal(f.key)
		compact.Write(key)
		compact.WriteByte(':')
		if err := json.Compact(&compact, f.value); err != nil {
			return nil, err
		}
	}
	compact.WriteByte('}')
	var out bytes.Buffer
	if err := json.Indent(&out, compact.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}
