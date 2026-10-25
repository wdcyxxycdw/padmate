package input

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"unicode/utf8"
)

// The JSON line format of events (ENG-08) is that of "padmatectl monitor
// --json": recordings ("padmatectl record", testdata/traces) and the fake
// input of padmated use it. One object per line:
//
//	{"timeNs":0,"kind":"added","device":1,"info":{"id":1,"name":"...",...}}
//	{"timeNs":5000000,"kind":"down","device":1,"control":"south"}
//	{"timeNs":9000000,"kind":"axis","device":1,"control":"lefttrigger","value":32767}
//
// timeNs is Event.Time, kind is EventKind.String of added, removed, down,
// up or axis (never combo: ComboHeld is not recorded), control is set for
// down, up and axis, value for axis only, and info, whose fields are the
// JSON tags of DeviceInfo, for added only. SDL does not reuse a device id
// within a process, so a gamepad that is removed and comes back has a
// new id; the info of its added events, such as guid, tells that it is
// the same gamepad. A gamepad that the receiver watchdog reported removed
// comes back with a new id as well (DeviceID); "padmatectl record" leaves
// the watchdog off.

// eventJSON is one line of the format.
type eventJSON struct {
	TimeNs  uint64      `json:"timeNs"`
	Kind    string      `json:"kind"`
	Device  DeviceID    `json:"device"`
	Control string      `json:"control,omitempty"`
	Value   *int16      `json:"value,omitempty"`
	Info    *DeviceInfo `json:"info,omitempty"`
}

// traceKinds are the kinds of the format, in the order of the error of
// kindField. ComboHeld is not one: a replay inserts it (ExpandCombos), so
// a trace that carried it would pause twice.
var traceKinds = [...]EventKind{DeviceAdded, DeviceRemoved, ButtonDown, ButtonUp, AxisMotion}

// WriteEvent writes e as one JSON line, with a single Write. It checks
// only the kind, which must be one of the format; ComboHeld is not. An
// Event from a Source, or from ReadEvents, of such a kind reads back the
// same, but for its Stamp: that is the time T-input received the event
// (DMN-15), which a trace does not keep.
func WriteEvent(w io.Writer, e Event) error {
	if !slices.Contains(traceKinds[:], e.Kind) {
		return fmt.Errorf("input: event kind %s is not written to traces", e.Kind)
	}
	j := eventJSON{TimeNs: e.Time, Kind: e.Kind.String(), Device: e.Device, Control: e.Control, Info: e.Info}
	if e.Kind == AxisMotion {
		v := e.Value
		j.Value = &v
	}
	enc := json.NewEncoder(w) // Encode writes the line at once
	enc.SetEscapeHTML(false)
	return enc.Encode(j)
}

// ReadEvents reads events in the JSON line format of WriteEvent. Blank
// lines and lines that start with "#", after spaces, are skipped, so a
// trace can carry comments; a UTF-8 BOM is ignored. It is strict: every
// line must be valid UTF-8 and one JSON object with only the fields of
// the format, each at most once, of the right type and range; timeNs,
// kind and device are required (timeNs is 0..2⁶³-1, the range of the
// engine's clock; device is 1..2³²-1), control is a button or axis name
// of this package (not an alias) and the value of a trigger is 0..32767.
// info may omit fields; info.id, when given, must equal device, and
// info.guid is "" or 32 lowercase hex digits, like DeviceInfo.GUID. The
// times of a device never decrease, and a device has no events after its
// removed event, as SDL does not reuse its id. The times of different
// devices may decrease, as SDL's backends time-stamp their events
// themselves, so a reader that replays picks one device and rebases its
// events to the first one's time.
//
// An error names the line, such as `line 3: unknown kind "press"`; there
// are no events with it.
func ReadEvents(r io.Reader) ([]Event, error) {
	type seen struct {
		t       uint64
		line    int
		removed bool // the line is the device's removed event
	}
	var (
		evs  []Event
		last = make(map[DeviceID]seen)
		n    int
	)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		n++
		line := sc.Bytes()
		if n == 1 {
			if bytes.HasPrefix(line, []byte("\xff\xfe")) || bytes.HasPrefix(line, []byte("\xfe\xff")) {
				return nil, errors.New("line 1: the file is UTF-16; save it as UTF-8")
			}
			line = bytes.TrimPrefix(line, []byte("\xef\xbb\xbf"))
		}
		e, ok, err := parseLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if !ok {
			continue
		}
		if p, ok := last[e.Device]; ok {
			if p.removed {
				return nil, fmt.Errorf("line %d: device %d was removed on line %d; SDL does not reuse device ids, so a gamepad that comes back has a new one",
					n, e.Device, p.line)
			}
			if e.Time < p.t {
				return nil, fmt.Errorf("line %d: timeNs %d is before the %d of line %d, the previous event of device %d",
					n, e.Time, p.t, p.line, e.Device)
			}
		}
		last[e.Device] = seen{e.Time, n, e.Kind == DeviceRemoved}
		evs = append(evs, e)
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, fmt.Errorf("line %d: longer than %d KiB", n+1, bufio.MaxScanTokenSize>>10)
		}
		return nil, fmt.Errorf("read after line %d: %w", n, err)
	}
	return evs, nil
}

// parseLine parses one line; ok is false for a blank or comment line.
func parseLine(line []byte) (e Event, ok bool, err error) {
	switch {
	case bytes.IndexByte(line, 0) >= 0:
		return e, false, errors.New("NUL byte: is the file UTF-16? Save it as UTF-8")
	case !utf8.Valid(line):
		return e, false, errors.New("not valid UTF-8; save the file as UTF-8")
	}
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] == '#' {
		return e, false, nil
	}
	e, err = parseEvent(trimmed)
	return e, err == nil, err
}

// parseEvent parses one JSON object of the format.
func parseEvent(data []byte) (Event, error) {
	var e Event
	ms, err := readObject(data, "")
	if err != nil {
		return e, err
	}
	var (
		haveTime, haveKind, haveDevice bool
		control, value, info           json.RawMessage // nil when absent
	)
	for _, m := range ms {
		switch m.key {
		case "timeNs":
			e.Time, err = uintField(m, 0, math.MaxInt64)
			haveTime = true
		case "kind":
			e.Kind, err = kindField(m)
			haveKind = true
		case "device":
			var d uint64
			d, err = uintField(m, 1, math.MaxUint32)
			e.Device, haveDevice = DeviceID(d), true
		case "control":
			control = m.raw
		case "value":
			value = m.raw
		case "info":
			info = m.raw
		default:
			err = fmt.Errorf("unknown field %q", m.key)
		}
		if err != nil {
			return e, err
		}
	}
	switch {
	case !haveTime:
		return e, errors.New("missing timeNs")
	case !haveKind:
		return e, errors.New("missing kind")
	case !haveDevice:
		return e, errors.New("missing device")
	}

	switch e.Kind {
	case ButtonDown, ButtonUp, AxisMotion:
		if control == nil {
			return e, errors.New("missing control")
		}
		if e.Control, err = stringField(member{"control", control}); err != nil {
			return e, err
		}
	default:
		if control != nil {
			return e, errors.New("control is only for down, up and axis")
		}
	}
	if e.Kind != AxisMotion && value != nil {
		return e, errors.New("value is only for axis")
	}
	switch e.Kind {
	case ButtonDown, ButtonUp:
		if _, ok := buttonIndex(e.Control); !ok {
			if _, isAxis := axisIndex(e.Control); isAxis {
				return e, fmt.Errorf("unknown button %q: it is an axis", e.Control)
			}
			return e, fmt.Errorf("unknown button %q", e.Control)
		}
	case AxisMotion:
		a, ok := axisIndex(e.Control)
		if !ok {
			if _, isButton := buttonIndex(e.Control); isButton {
				return e, fmt.Errorf("unknown axis %q: it is a button", e.Control)
			}
			return e, fmt.Errorf("unknown axis %q", e.Control)
		}
		if value == nil {
			return e, errors.New("missing value")
		}
		lo, what := int64(math.MinInt16), ""
		if isTrigger(a) {
			lo, what = 0, " for a trigger"
		}
		v, err := intField(member{"value", value}, lo, math.MaxInt16, what)
		if err != nil {
			return e, err
		}
		e.Value = int16(v)
	}
	if info != nil {
		if e.Kind != DeviceAdded {
			return e, errors.New("info is only for added")
		}
		if e.Info, err = parseInfo(info, e.Device); err != nil {
			return e, err
		}
	}
	return e, nil
}

// parseInfo parses the info object of the DeviceAdded event of device
// dev. Missing fields are zero, and a missing id is dev.
func parseInfo(raw json.RawMessage, dev DeviceID) (*DeviceInfo, error) {
	if raw[0] != '{' {
		return nil, fmt.Errorf("info: want an object, got %s", got(raw))
	}
	ms, err := readObject(raw, "info.")
	if err != nil {
		return nil, err
	}
	d := &DeviceInfo{ID: dev}
	for _, m := range ms {
		var err error
		switch m.key {
		case "info.id":
			var id uint64
			if id, err = uintField(m, 1, math.MaxUint32); err == nil && DeviceID(id) != dev {
				err = fmt.Errorf("info.id is %d, not the device %d", id, dev)
			}
		case "info.name":
			d.Name, err = stringField(m)
		case "info.type":
			d.Type, err = stringField(m)
		case "info.buttonLabels":
			d.ButtonLabels, err = readButtonLabelsJSON(m.raw)
		case "info.vendor":
			d.Vendor, err = uint16Field(m)
		case "info.product":
			d.Product, err = uint16Field(m)
		case "info.version":
			d.Version, err = uint16Field(m)
		case "info.guid":
			// DeviceKey.String relies on a GUID being hex.
			if d.GUID, err = stringField(m); err == nil && d.GUID != "" && !isGUIDString(d.GUID) {
				err = fmt.Errorf(`info.guid: want "" or 32 lowercase hex digits, got %q`, d.GUID)
			}
		case "info.driver":
			d.Driver, err = stringField(m)
		case "info.path":
			d.Path, err = stringField(m)
		case "info.serial":
			d.Serial, err = stringField(m)
		case "info.gamepad":
			d.IsGamepad, err = boolField(m)
		case "info.virtual":
			d.Virtual, err = boolField(m)
		case "info.buttonMask":
			var mask uint64
			mask, err = uintField(m, 0, math.MaxUint32)
			d.ButtonMask = uint32(mask)
		case "info.axisMask":
			var mask uint64
			mask, err = uintField(m, 0, math.MaxUint8)
			d.AxisMask = uint8(mask)
		case "info.capabilities":
			d.Capabilities, err = readCapabilitiesJSON(m.raw)
		default:
			err = fmt.Errorf("unknown field %q", m.key)
		}
		if err != nil {
			return nil, err
		}
	}
	return d, nil
}

func readButtonLabelsJSON(raw []byte) (FaceButtonLabels, error) {
	var labels FaceButtonLabels
	members, err := readObject(raw, "info.buttonLabels.")
	if err != nil {
		return labels, err
	}
	for _, m := range members {
		var dest *string
		switch m.key {
		case "info.buttonLabels.south":
			dest = &labels.South
		case "info.buttonLabels.east":
			dest = &labels.East
		case "info.buttonLabels.west":
			dest = &labels.West
		case "info.buttonLabels.north":
			dest = &labels.North
		default:
			return labels, fmt.Errorf("unknown field %q", m.key)
		}
		*dest, err = stringField(m)
		if err != nil {
			return labels, err
		}
	}
	return labels, nil
}

func readCapabilitiesJSON(raw []byte) (Capabilities, error) {
	var c Capabilities
	members, err := readObject(raw, "info.capabilities.")
	if err != nil {
		return c, err
	}
	for _, m := range members {
		var dest *bool
		switch m.key {
		case "info.capabilities.known":
			dest = &c.Known
		case "info.capabilities.gyro":
			dest = &c.Gyro
		case "info.capabilities.accelerometer":
			dest = &c.Accelerometer
		case "info.capabilities.rumble":
			dest = &c.Rumble
		case "info.capabilities.triggerRumble":
			dest = &c.TriggerRumble
		case "info.capabilities.led":
			dest = &c.LED
		case "info.capabilities.rgbLed":
			dest = &c.RGBLED
		case "info.capabilities.playerLed":
			dest = &c.PlayerLED
		case "info.capabilities.touchpads":
			var n uint64
			n, err = uintField(m, 0, math.MaxInt32)
			c.Touchpads = int32(n)
		default:
			return c, fmt.Errorf("unknown field %q", m.key)
		}
		if dest != nil {
			*dest, err = boolField(m)
		}
		if err != nil {
			return c, err
		}
	}
	return c, nil
}

// member is a field of a JSON object: its key, with the prefix that
// readObject was given, and its raw value.
type member struct {
	key string
	raw json.RawMessage
}

// readObject reads data, which must be exactly one JSON object, and
// returns its fields in order. Unlike encoding/json it rejects duplicate
// keys instead of keeping the last one.
func readObject(data []byte, prefix string) ([]member, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if tok != json.Delim('{') {
		return nil, errors.New("want a JSON object")
	}
	var ms []member
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok { // not reached: the decoder only returns string keys
			return nil, fmt.Errorf("key %v is not a string", tok)
		}
		key = prefix + key
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		for _, m := range ms {
			if m.key == key {
				return nil, fmt.Errorf("duplicate field %q", key)
			}
		}
		ms = append(ms, member{key, raw})
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, err
	}
	switch _, err := dec.Token(); {
	case err == nil:
		return nil, errors.New("data after the object")
	case !errors.Is(err, io.EOF):
		return nil, err
	}
	return ms, nil
}

// got describes a raw JSON value for an error: a number as itself,
// anything else by its type.
func got(raw json.RawMessage) string {
	switch raw[0] {
	case '"':
		return "a string"
	case '{':
		return "an object"
	case '[':
		return "an array"
	case 't', 'f':
		return "a boolean"
	case 'n':
		return "null"
	default:
		return string(raw)
	}
}

func uintField(m member, lo, hi uint64) (uint64, error) {
	n, err := strconv.ParseUint(string(m.raw), 10, 64)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("%s: want an integer from %d to %d, got %s", m.key, lo, hi, got(m.raw))
	}
	return n, nil
}

func uint16Field(m member) (uint16, error) {
	n, err := uintField(m, 0, math.MaxUint16)
	return uint16(n), err
}

// intField parses an integer in lo..hi; what follows the range in the
// error.
func intField(m member, lo, hi int64, what string) (int64, error) {
	n, err := strconv.ParseInt(string(m.raw), 10, 64)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("%s: want an integer from %d to %d%s, got %s", m.key, lo, hi, what, got(m.raw))
	}
	return n, nil
}

func stringField(m member) (string, error) {
	var s string
	if m.raw[0] != '"' || json.Unmarshal(m.raw, &s) != nil {
		return "", fmt.Errorf("%s: want a string, got %s", m.key, got(m.raw))
	}
	return s, nil
}

func boolField(m member) (bool, error) {
	switch string(m.raw) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("%s: want true or false, got %s", m.key, got(m.raw))
}

func kindField(m member) (EventKind, error) {
	name, err := stringField(m)
	if err != nil {
		return 0, err
	}
	for _, k := range traceKinds {
		if k.String() == name {
			return k, nil
		}
	}
	return 0, fmt.Errorf("unknown kind %q; want added, removed, down, up or axis", name)
}
