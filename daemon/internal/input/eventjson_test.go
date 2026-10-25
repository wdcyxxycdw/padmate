package input_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// xbox is a DeviceInfo with every field set.
var xbox = &input.DeviceInfo{
	ID: 3, Name: "Xbox Wireless Controller", Type: "xboxone",
	Vendor: 0x045e, Product: 0x0b13, Version: 0x0509,
	GUID: "0500a1b25e040000130b000009057200", Driver: "hidapi",
	Path: `\\?\HID#VID_045E&PID_0B13`, Serial: "abc", IsGamepad: true,
}

// eventLines are events and their JSON lines, which are also those of
// "padmatectl monitor --json".
var eventLines = []struct {
	e    input.Event
	line string
}{
	{
		input.Event{Time: 1_234_567_890, Device: 3, Kind: input.DeviceAdded, Info: xbox},
		`{"timeNs":1234567890,"kind":"added","device":3,"info":{"id":3,"name":"Xbox Wireless Controller","type":"xboxone","vendor":1118,"product":2835,"version":1289,"guid":"0500a1b25e040000130b000009057200","driver":"hidapi","path":"\\\\?\\HID#VID_045E&PID_0B13","serial":"abc","gamepad":true,"virtual":false}}`,
	},
	{
		input.Event{Time: 5, Device: 4, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 4, Name: "<Wheel>", Type: "unknown", Driver: "dinput", Virtual: true}},
		`{"timeNs":5,"kind":"added","device":4,"info":{"id":4,"name":"<Wheel>","type":"unknown","vendor":0,"product":0,"version":0,"guid":"","driver":"dinput","path":"","serial":"","gamepad":false,"virtual":true}}`,
	},
	{
		input.Event{Time: 7, Device: 5, Kind: input.DeviceAdded},
		`{"timeNs":7,"kind":"added","device":5}`,
	},
	{
		input.Event{Time: 2_000_001_000, Device: 3, Kind: input.ButtonDown, Control: "south"},
		`{"timeNs":2000001000,"kind":"down","device":3,"control":"south"}`,
	},
	{
		input.Event{Time: 2_500_000_000, Device: 3, Kind: input.ButtonUp, Control: "dpad.up"},
		`{"timeNs":2500000000,"kind":"up","device":3,"control":"dpad.up"}`,
	},
	{
		input.Event{Time: 3_000_000_000, Device: 3, Kind: input.AxisMotion, Control: "leftx", Value: -32768},
		`{"timeNs":3000000000,"kind":"axis","device":3,"control":"leftx","value":-32768}`,
	},
	{
		input.Event{Time: 3_000_000_000, Device: 3, Kind: input.AxisMotion, Control: "lefttrigger"},
		`{"timeNs":3000000000,"kind":"axis","device":3,"control":"lefttrigger","value":0}`,
	},
	{
		input.Event{Time: 9223372036854775807, Device: 4294967295, Kind: input.DeviceRemoved},
		`{"timeNs":9223372036854775807,"kind":"removed","device":4294967295}`,
	},
}

func TestWriteEvent(t *testing.T) {
	for _, tt := range eventLines {
		var b bytes.Buffer
		w := &countWriter{w: &b}
		if err := input.WriteEvent(w, tt.e); err != nil {
			t.Fatal(err)
		}
		if got := b.String(); got != tt.line+"\n" {
			t.Errorf("WriteEvent(%+v):\n got %q\nwant %q", tt.e, got, tt.line+"\n")
		}
		if w.n != 1 {
			t.Errorf("WriteEvent(%+v) called Write %d times, want once", tt.e, w.n)
		}
	}
}

// countWriter counts the Write calls.
type countWriter struct {
	w *bytes.Buffer
	n int
}

func (c *countWriter) Write(p []byte) (int, error) {
	c.n++
	return c.w.Write(p)
}

func TestReadEventLines(t *testing.T) {
	var text strings.Builder
	var want []input.Event
	for _, tt := range eventLines {
		evs, err := input.ReadEvents(strings.NewReader(tt.line))
		if err != nil || len(evs) != 1 || !reflect.DeepEqual(evs[0], tt.e) {
			t.Errorf("ReadEvents(%s) = %+v, %v; want %+v", tt.line, evs, err, tt.e)
		}
		text.WriteString(tt.line + "\n")
		want = append(want, tt.e)
	}
	evs, err := input.ReadEvents(strings.NewReader(text.String()))
	if err != nil || !reflect.DeepEqual(evs, want) {
		t.Errorf("ReadEvents(all lines) = %+v, %v; want %+v", evs, err, want)
	}
}

func TestReadEvents(t *testing.T) {
	down := input.Event{Time: 10, Device: 1, Kind: input.ButtonDown, Control: "south"}
	tests := []struct {
		name, text string
		want       []input.Event
	}{
		{"empty", "", nil},
		{"only comments", "# a trace\n\n   # indented\r\n\t\n", nil},
		{"comments around", "# head\n" + `{"timeNs":10,"kind":"down","device":1,"control":"south"}` + "\n\n# tail", []input.Event{down}},
		{"CRLF", `{"timeNs":10,"kind":"down","device":1,"control":"south"}` + "\r\n", []input.Event{down}},
		{"UTF-8 BOM", "\xef\xbb\xbf" + `{"timeNs":10,"kind":"down","device":1,"control":"south"}`, []input.Event{down}},
		{"UTF-8 BOM before a comment", "\xef\xbb\xbf# 录制\n" + `{"timeNs":10,"kind":"down","device":1,"control":"south"}`, []input.Event{down}},
		{"any field order and spacing", ` { "control" : "south", "device":1 ,"kind":"down","timeNs":10 } `, []input.Event{down}},
		{
			"partial info", `{"timeNs":0,"kind":"added","device":7,"info":{"name":"pad","gamepad":true}}`,
			[]input.Event{{Device: 7, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 7, Name: "pad", IsGamepad: true}}},
		},
		{
			"empty info", `{"timeNs":0,"kind":"added","device":7,"info":{}}`,
			[]input.Event{{Device: 7, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 7}}},
		},
		{
			"equal times", `{"timeNs":10,"kind":"down","device":1,"control":"south"}` + "\n" + `{"timeNs":10,"kind":"up","device":1,"control":"south"}`,
			[]input.Event{down, {Time: 10, Device: 1, Kind: input.ButtonUp, Control: "south"}},
		},
		{
			// Each device has its own clock: SDL's backends time-stamp
			// their events themselves.
			"times of another device", `{"timeNs":10,"kind":"down","device":1,"control":"south"}` + "\n" + `{"timeNs":5,"kind":"down","device":2,"control":"east"}`,
			[]input.Event{down, {Time: 5, Device: 2, Kind: input.ButtonDown, Control: "east"}},
		},
		{
			"trigger and stick ranges",
			`{"timeNs":0,"kind":"axis","device":1,"control":"righttrigger","value":32767}` + "\n" +
				`{"timeNs":0,"kind":"axis","device":1,"control":"righty","value":-32768}`,
			[]input.Event{
				{Device: 1, Kind: input.AxisMotion, Control: "righttrigger", Value: 32767},
				{Device: 1, Kind: input.AxisMotion, Control: "righty", Value: -32768},
			},
		},
		{"escaped name", `{"timeNs":10,"kind":"down","device":1,"control":"sout\u0068"}`, []input.Event{down}},
	}
	for _, tt := range tests {
		got, err := input.ReadEvents(strings.NewReader(tt.text))
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: ReadEvents = %+v, %v; want %+v", tt.name, got, err, tt.want)
		}
	}
}

// badLines are lines that ReadEvents rejects, and the error it gives.
var badLines = []struct {
	text, err string
}{
	{`{"timeNs":0,"kind":"press","device":1}`, `line 1: unknown kind "press"; want added, removed, down, up or axis`},
	{`{"timeNs":0,"kind":"kind1","device":1}`, `line 1: unknown kind "kind1"`},
	// ComboHeld is inserted by a replay, never read from a trace.
	{`{"timeNs":0,"kind":"combo","device":1}`, `line 1: unknown kind "combo"; want added, removed, down, up or axis`},
	// DeviceState and DevicePower are routing metadata of a Source as well.
	{`{"timeNs":0,"kind":"state","device":1}`, `line 1: unknown kind "state"; want added, removed, down, up or axis`},
	{`{"timeNs":0,"kind":"power","device":1}`, `line 1: unknown kind "power"; want added, removed, down, up or axis`},
	{`{"timeNs":0,"kind":"power","device":1,"power":{"state":"discharging","percent":80}}`, `line 1: unknown kind "power"; want added, removed, down, up or axis`},
	{`{"timeNs":0,"kind":"removed","device":1,"x":1}`, `line 1: unknown field "x"`},
	{`{"TimeNs":0,"kind":"removed","device":1}`, `line 1: unknown field "TimeNs"`},
	{`{"timeNs":0,"timeNs":1,"kind":"removed","device":1}`, `line 1: duplicate field "timeNs"`},
	{`{"kind":"removed","device":1}`, `line 1: missing timeNs`},
	{`{"timeNs":0,"device":1}`, `line 1: missing kind`},
	{`{"timeNs":0,"kind":"removed"}`, `line 1: missing device`},
	{`{"timeNs":"0","kind":"removed","device":1}`, `line 1: timeNs: want an integer from 0 to 9223372036854775807, got a string`},
	{`{"timeNs":-1,"kind":"removed","device":1}`, `line 1: timeNs: want an integer from 0 to 9223372036854775807, got -1`},
	{`{"timeNs":1.5,"kind":"removed","device":1}`, `got 1.5`},
	{`{"timeNs":1e3,"kind":"removed","device":1}`, `got 1e3`},
	{`{"timeNs":9223372036854775808,"kind":"removed","device":1}`, `line 1: timeNs: want an integer from 0 to 9223372036854775807, got 9223372036854775808`},
	{`{"timeNs":18446744073709551615,"kind":"removed","device":1}`, `got 18446744073709551615`},
	{`{"timeNs":18446744073709551616,"kind":"removed","device":1}`, `got 18446744073709551616`},
	{`{"timeNs":null,"kind":"removed","device":1}`, `line 1: timeNs: want an integer from 0 to 9223372036854775807, got null`},
	{`{"timeNs":0,"kind":"removed","device":0}`, `line 1: device: want an integer from 1 to 4294967295, got 0`},
	{`{"timeNs":0,"kind":"removed","device":4294967296}`, `device: want an integer from 1 to 4294967295, got 4294967296`},
	{`{"timeNs":0,"kind":"removed","device":true}`, `device: want an integer from 1 to 4294967295, got a boolean`},
	{`{"timeNs":0,"kind":2,"device":1}`, `line 1: kind: want a string, got 2`},
	{`{"timeNs":0,"kind":["down"],"device":1}`, `kind: want a string, got an array`},
	{`{"timeNs":0,"kind":"down","device":1}`, `line 1: missing control`},
	{`{"timeNs":0,"kind":"axis","device":1,"value":0}`, `line 1: missing control`},
	{`{"timeNs":0,"kind":"down","device":1,"control":{}}`, `line 1: control: want a string, got an object`},
	{`{"timeNs":0,"kind":"added","device":1,"control":"south"}`, `line 1: control is only for down, up and axis`},
	{`{"timeNs":0,"kind":"removed","device":1,"control":""}`, `line 1: control is only for down, up and axis`},
	{`{"timeNs":0,"kind":"down","device":1,"control":"a"}`, `line 1: unknown button "a"`},
	{`{"timeNs":0,"kind":"up","device":1,"control":""}`, `line 1: unknown button ""`},
	{`{"timeNs":0,"kind":"down","device":1,"control":"South"}`, `line 1: unknown button "South"`},
	{`{"timeNs":0,"kind":"down","device":1,"control":"lefttrigger"}`, `line 1: unknown button "lefttrigger": it is an axis`},
	{`{"timeNs":0,"kind":"axis","device":1,"control":"south","value":0}`, `line 1: unknown axis "south": it is a button`},
	{`{"timeNs":0,"kind":"axis","device":1,"control":"lt","value":0}`, `line 1: unknown axis "lt"`},
	{`{"timeNs":0,"kind":"axis","device":1,"control":"leftx"}`, `line 1: missing value`},
	{`{"timeNs":0,"kind":"down","device":1,"control":"south","value":1}`, `line 1: value is only for axis`},
	{`{"timeNs":0,"kind":"added","device":1,"value":0}`, `line 1: value is only for axis`},
	{`{"timeNs":0,"kind":"axis","device":1,"control":"leftx","value":32768}`, `line 1: value: want an integer from -32768 to 32767, got 32768`},
	{`{"timeNs":0,"kind":"axis","device":1,"control":"leftx","value":-32769}`, `got -32769`},
	{`{"timeNs":0,"kind":"axis","device":1,"control":"leftx","value":0.5}`, `got 0.5`},
	{`{"timeNs":0,"kind":"axis","device":1,"control":"leftx","value":"1"}`, `value: want an integer from -32768 to 32767, got a string`},
	{`{"timeNs":0,"kind":"axis","device":1,"control":"lefttrigger","value":-1}`, `line 1: value: want an integer from 0 to 32767 for a trigger, got -1`},
	{`{"timeNs":0,"kind":"down","device":1,"control":"south","info":{}}`, `line 1: info is only for added`},
	{`{"timeNs":0,"kind":"removed","device":1,"info":{}}`, `line 1: info is only for added`},
	{`{"timeNs":0,"kind":"added","device":1,"info":"pad"}`, `line 1: info: want an object, got a string`},
	{`{"timeNs":0,"kind":"added","device":1,"info":null}`, `line 1: info: want an object, got null`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"x":1}}`, `line 1: unknown field "info.x"`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"Name":"a"}}`, `line 1: unknown field "info.Name"`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"name":"a","name":"b"}}`, `line 1: duplicate field "info.name"`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"id":2}}`, `line 1: info.id is 2, not the device 1`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"id":-1}}`, `line 1: info.id: want an integer from 1 to 4294967295, got -1`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"vendor":65536}}`, `line 1: info.vendor: want an integer from 0 to 65535, got 65536`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"product":"1"}}`, `line 1: info.product: want an integer from 0 to 65535, got a string`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"version":1.0}}`, `line 1: info.version: want an integer from 0 to 65535, got 1.0`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"name":null}}`, `line 1: info.name: want a string, got null`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"guid":1}}`, `line 1: info.guid: want a string, got 1`},
	// DeviceKey.String relies on a GUID being hex: the GUID "ab/s:x" would
	// show as the key of the GUID "ab" with the serial "x".
	{`{"timeNs":0,"kind":"added","device":1,"info":{"guid":"ab/s:x"}}`, `line 1: info.guid: want "" or 32 lowercase hex digits, got "ab/s:x"`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"guid":"0500a1b25e040000130b000009057200#1"}}`, `info.guid: want "" or 32 lowercase hex digits`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"guid":"0500A1B25E040000130B000009057200"}}`, `info.guid: want "" or 32 lowercase hex digits`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"guid":"0500a1b25e040000130b00000905720"}}`, `info.guid: want "" or 32 lowercase hex digits`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"guid":"0500a1b25e040000130b00000905720g"}}`, `info.guid: want "" or 32 lowercase hex digits`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"gamepad":"true"}}`, `line 1: info.gamepad: want true or false, got a string`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"virtual":1}}`, `line 1: info.virtual: want true or false, got 1`},
	{`[]`, `line 1: want a JSON object`},
	{`"added"`, `line 1: want a JSON object`},
	{`null`, `line 1: want a JSON object`},
	{`{`, `line 1: `},
	{`{"timeNs":0,}`, `line 1: invalid character`},
	{`{"timeNs" 0}`, `line 1: invalid character`},
	{`{"timeNs":0,"kind":"removed","device":1} x`, `line 1: invalid character 'x'`},
	{`{"timeNs":0,"kind":"removed","device":1}{}`, `line 1: data after the object`},
	{`{"timeNs":0,"kind":"removed","device":1}}`, `line 1: invalid character '}'`},
	{`{"timeNs":0,"kind":"removed","device":1,"x":[1,}`, `line 1: invalid character`},
	{"\xff\xfe{\x00}\x00", `line 1: the file is UTF-16; save it as UTF-8`},
	{"\xfe\xff\x00{\x00}", `line 1: the file is UTF-16; save it as UTF-8`},
	{"{\x00}\x00", `line 1: NUL byte: is the file UTF-16? Save it as UTF-8`},
	{`{"timeNs":0,"kind":"added","device":1,"info":{"name":"` + "\xff" + `"}}`, `line 1: not valid UTF-8`},
	{"# \xc4\xe3\xba\xc3\n", `line 1: not valid UTF-8`},
	{"# a\n\n" + `{"timeNs":0,"kind":"press","device":1}`, `line 3: unknown kind "press"`},
	{
		`{"timeNs":10,"kind":"down","device":1,"control":"south"}` + "\n" +
			`{"timeNs":5,"kind":"down","device":2,"control":"south"}` + "\n# c\n" +
			`{"timeNs":9,"kind":"up","device":1,"control":"south"}`,
		`line 4: timeNs 9 is before the 10 of line 1, the previous event of device 1`,
	},
	{
		// SDL does not reuse ids: a gamepad that comes back has a new one.
		`{"timeNs":0,"kind":"added","device":1}` + "\n" + `{"timeNs":5,"kind":"removed","device":1}` + "\n" +
			`{"timeNs":6,"kind":"added","device":2}` + "\n" + `{"timeNs":7,"kind":"added","device":1}`,
		`line 4: device 1 was removed on line 2; SDL does not reuse device ids, so a gamepad that comes back has a new one`,
	},
	{`{"timeNs":5,"kind":"removed","device":1}` + "\n# c\n" + `{"timeNs":9,"kind":"down","device":1,"control":"south"}`, `line 3: device 1 was removed on line 1`},
	{`{"timeNs":5,"kind":"removed","device":1}` + "\n" + `{"timeNs":5,"kind":"removed","device":1}`, `line 2: device 1 was removed on line 1`},
	{"\n" + `{"timeNs":0,"kind":"removed","device":1}` + "\n" + strings.Repeat(" ", 70000), `line 3: longer than 64 KiB`},
	{"\n\xef\xbb\xbf" + `{"timeNs":0,"kind":"removed","device":1}`, `line 2: invalid character`},
}

func TestReadEventsErrors(t *testing.T) {
	for _, tt := range badLines {
		evs, err := input.ReadEvents(strings.NewReader(tt.text))
		if err == nil || !strings.Contains(err.Error(), tt.err) || !strings.HasPrefix(err.Error(), "line ") {
			t.Errorf("ReadEvents(%q) = %+v, %v; want the error %q", tt.text, evs, err, tt.err)
		}
		if evs != nil {
			t.Errorf("ReadEvents(%q) returned events with its error", tt.text)
		}
	}
}

// TestReadEventsInfoFields checks that ReadEvents reads every field of
// DeviceInfo that WriteEvent writes: a new field fails here until
// ReadEvents knows it.
func TestReadEventsInfoFields(t *testing.T) {
	d := &input.DeviceInfo{}
	v := reflect.ValueOf(d).Elem()
	for i := range v.NumField() {
		f := v.Field(i)
		switch name := v.Type().Field(i).Name; f.Kind() {
		case reflect.String:
			f.SetString(name + ` "<&>" 手柄`)
			if name == "GUID" { // it must be hex
				f.SetString("0123456789abcdef0123456789abcdef")
			}
		case reflect.Uint16:
			f.SetUint(uint64(1000 + i))
		case reflect.Uint32:
			if name == "ID" {
				f.SetUint(9)
			} else {
				f.SetUint(12345)
			}
		case reflect.Uint8:
			f.SetUint(63)
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Struct:
			if name != "Capabilities" && name != "ButtonLabels" {
				t.Fatalf("unknown nested DeviceInfo field %s", name)
			}
			for j := range f.NumField() {
				switch field := f.Field(j); field.Kind() {
				case reflect.Bool:
					field.SetBool(true)
				case reflect.Int32:
					field.SetInt(2)
				case reflect.String:
					field.SetString("A")
				default:
					t.Fatalf("%s.%s: add a test case", name, f.Type().Field(j).Name)
				}
			}
		default:
			t.Fatalf("DeviceInfo.%s: add a case for %s here and in ReadEvents", name, f.Kind())
		}
	}
	e := input.Event{Time: 1, Device: 9, Kind: input.DeviceAdded, Info: d}
	var b bytes.Buffer
	if err := input.WriteEvent(&b, e); err != nil {
		t.Fatal(err)
	}
	evs, err := input.ReadEvents(&b)
	if err != nil || len(evs) != 1 || !reflect.DeepEqual(evs[0], e) {
		t.Errorf("ReadEvents(WriteEvent(%+v)) = %+v, %v", *d, evs, err)
	}
}

// lineErr is the form of every error of ReadEvents on a bytes.Reader.
var lineErr = regexp.MustCompile(`^line ([1-9][0-9]*): `)

// FuzzReadEvents checks that ReadEvents does not panic, that its errors
// name a line of the input, and that the events it reads survive
// WriteEvent and ReadEvents again unchanged.
func FuzzReadEvents(f *testing.F) {
	f.Add([]byte(`{"timeNs":0,"kind":"added","device":1,"info":{"buttonLabels":{"south":"Cross","east":"Circle","west":"Square","north":"Triangle"}}}`))
	f.Add([]byte(`{"timeNs":0,"kind":"added","device":1,"info":{"buttonLabels":{"south":false}}}`))
	f.Add([]byte(`{"timeNs":0,"kind":"added","device":1,"info":{"capabilities":{"known":true,"gyro":true,"accelerometer":false,"touchpads":2,"rumble":true,"triggerRumble":false,"led":true,"rgbLed":true,"playerLed":true}}}`))
	for _, tt := range eventLines {
		f.Add([]byte(tt.line))
	}
	for _, tt := range badLines {
		f.Add([]byte(tt.text))
	}
	// The recorded traces (ENG-08).
	root := filepath.Join("..", "..", "testdata", "traces")
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(path) == ".jsonl" {
			if b, err := os.ReadFile(path); err == nil {
				f.Add(b)
			}
		}
		return nil
	})
	f.Fuzz(func(t *testing.T, data []byte) {
		evs, err := input.ReadEvents(bytes.NewReader(data))
		if err != nil {
			m := lineErr.FindStringSubmatch(err.Error())
			if m == nil {
				t.Fatalf("error %q does not start with the line", err)
			}
			if n, _ := strconv.Atoi(m[1]); n > bytes.Count(data, []byte("\n"))+1 {
				t.Fatalf("error %q names a line past the end", err)
			}
			if evs != nil {
				t.Fatalf("events with the error %q", err)
			}
			return
		}
		var b bytes.Buffer
		for _, e := range evs {
			if err := input.WriteEvent(&b, e); err != nil {
				t.Fatal(err)
			}
		}
		again, err := input.ReadEvents(bytes.NewReader(b.Bytes()))
		if err != nil {
			t.Fatalf("reading the written events: %v\n%s", err, b.Bytes())
		}
		if !reflect.DeepEqual(again, evs) {
			t.Fatalf("written and read again:\n got %+v\nwant %+v", again, evs)
		}
	})
}
