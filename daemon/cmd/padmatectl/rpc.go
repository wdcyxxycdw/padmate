package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// TestMain points default discovery at a process-private nonexistent path.
var ipcDiscoveryDir string
var rpcInvoke = invokeRPC

const rpcUsage = `Daemon commands (flags precede positional arguments):
  status [--device ID]
  devices [--device ID]
  devices mapping set [--device ID] MAPPING|@FILE
  devices mapping reset [--device ID]
  identify [--device ID]
  latency [--device ID] [--reset]
  pause | resume
  layer [--device ID] switch|enable|disable|toggle LAYER
  profile list
  profile get ID
  profile resolve ID
  profile put --revision REV ID FILE
  profile copy FROM TO
  profile delete --revision REV ID
  profile history ID
  profile restore --revision REV ID VERSION
  profile export [--force] ID FILE
  profile import [--name NAME | --replace ID] [--macros replace|keep] FILE
  profile import-amgp [--name NAME] [--json] FILE
  profile activate ID | profile deactivate
  profile explain [--device ID | --id PROFILE] CONTROL
  config get
  config put --revision REV FILE
  script status|registrations|reload [--device ID]
  script describe [--id PROFILE]
  script init --id PROFILE
  script import [--replace] PROFILE FILE
  script logs [--device ID] [--after N] [--limit 100]
  macro list
  macro new NAME
  macro import [--replace] FILE
  macro rename FILE TO
  macro delete FILE
  fake press|release CONTROL
  fake axis AXIS INTEGER
  fake effects [--clear]
  fake battery STATE [PERCENT]
  rpc METHOD [JSON-PARAMS]

Common flags: --ipc-dir DIR, --timeout 10s, --json (compact output).
The default discovery file belongs to the current user. Authentication is
automatic; its token is never printed. Exit codes: 0 success, 1 RPC/I/O
failure, 2 invalid arguments. All results are JSON.
An empty --revision= only creates a missing document. Read the revision
with profile/config get before updating. Global config changes report
restartRequired; profile put reports whether the saved file was applied.
profile get shows the profile.json as it is, with, when it names a parent in
"extends", the parent and the chain above it. profile resolve shows the
profile whole: "text" is the profile with its parents merged in (no
"extends"), and "inherited" lists the members that come from a parent, as
JSON pointers with the parent's ID. An "extends" profile builds on another
profile of the library: it writes only what it changes; a control it writes
replaces the parent's whole control.
profile copy duplicates the folder, scripts included, to a new ID; a copy
of a profile that extends another extends it too. profile
delete permanently deletes the folder, scripts included, of an inactive
profile that neither config.json nor padmated's startup selects; it cannot
be undone. The profiles that extend the deleted one are first written whole
(the merge of their chain, no "extends"); "flattened" in the result lists
them. --revision= deletes a folder whose profile.json is missing or
unreadable.
profile history lists the earlier versions of a profile's profile.json,
newest first; padmated keeps the replaced version on every save, at most
30. profile restore saves one of them as profile.json like profile put
(the current file becomes a version first); VERSION is from profile
history.
profile export writes the profile folder, scripts included, and the
macros of the script library that it runs as an archive of at most 4 MiB
to FILE (a profile that extends another is written whole, without the
extends) (conventionally NAME.padmate); --force overwrites an existing
FILE. profile import adds such an archive as a new profile, its ID made
from its name; --name gives it another name, and --replace replaces the
folder of an existing profile instead (the default keeps its name). A
name another profile goes by is refused (-32021). The archive's macros go
into the script library; one whose name the library has with other bytes
is refused (-32022) unless --macros replace writes it over the
library's, or --macros keep adds it under a numbered name that the
imported profile runs.
script init --id writes main.ts, which binds no control, tsconfig.json and
the declarations into a library profile's folder, keeping existing files.
script import copies FILE, at most 1 MiB of UTF-8, into the profile folder
under its base name, which must end in .ts or .js; --replace replaces an
existing script, otherwise it fails. Neither edits profile.json: set
script.entry with profile put.
macro manages the script library, the macros that every library profile
runs: new creates NAME.ts from the template; import copies FILE, at most
1 MiB of UTF-8, under its base name, and --replace replaces a macro of the
same name and extension; rename keeps the extension and renames every
action of every profile that runs the macro; delete removes those
actions, then the file, which cannot be undone. rename and delete edit
each profile.json that runs the macro and apply the active one.
profile import-amgp makes a new profile from an AntiMicroX file (.amgp, or the
.xml of older versions; only game controller profiles of configversion 6 or
more) and prints what it could not carry over: every line says dropped
(丢弃), approximate (近似) or info (信息), where in the file (set, control,
slot) and why. The name is --name, else the file's profilename, else the file's
name; a name from the file is numbered if another profile has it ("name 2"),
one from --name is refused (-32021). Several AntiMicroX sets become layers;
the profile only ever holds what padmate can run, and is checked like any
other. --json prints {id,name,report:[...],revision,applied} instead of the
text. A file that is refused (-32024) writes nothing.
devices mapping set gives SDL a gamepad mapping for a controller (IN-18): a joystick
that SDL does not know becomes a gamepad at once, and one it knows is read
differently. MAPPING is an SDL mapping string, "GUID,name,a:b0,b:b1,...", whose
GUID is the controller's (devices lists it); @FILE reads it from a file of at
most 8 KiB. The daemon saves it with the name *, no type field and
platform:Windows, in config.json under every device key with that GUID (SDL
applies a mapping by GUID), and applies it at once; devices mapping reset takes
it away again. The result says which keys were written, what SDL reads back for
the controllers with that GUID, and warns (noBack, noStart) when View or Menu
is not mapped, which the emergency combo and the function keys need, shadowed
when SDL reads another mapping (the user's SDL_GAMECONTROLLERCONFIG wins), or
notGamepad. --device is optional with one controller connected.
release-all remains an independent emergency event, without IPC.
identify makes one controller buzz and flash for at most a second and a half,
so that you can tell which it is, and prints which cues it gave as
{rumble,led,player}; all false means it can show nothing. ID is a
connection ID or a DeviceKey, as devices lists them, and --device is
optional with one controller connected. It works whatever the mapping and
the profile do, while paused too. fake effects prints the effect calls the
fake gamepad received (padmated --fake-input), oldest first; --clear
forgets them. fake battery makes the fake gamepad report a power, as a
real one does: STATE is wired, charging, discharging or unknown, and
PERCENT is 0 to 100 (an unknown state has none; leave it out for a
gamepad that does not report a charge).
latency prints, for each mapped controller, how long padmated took from
receiving the input edge to the return of the call that sent its keys, over
the last 1000 keys it sent: the number of samples and the p50, p90, p99 and
max in microseconds (ARCHITECTURE section 13), with dryRun and the threshold
that makes a p99 a warning. Keys that a timer sends (repeats, a hold that
just became one, a release that waits out the minimum press) and keys that
were only previewed, because padmate's own window was in front, are not
counted. --reset forgets the samples of the controller --device names, or of
all; ID is a connection ID or a DeviceKey.`

// flagGiven reports whether the command line set the flag name.
func flagGiven(fs *flag.FlagSet, name string) bool {
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == name })
	return given
}

func rpcCommand(name string) func([]string, io.Writer, io.Writer) int {
	return func(args []string, stdout, stderr io.Writer) int {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		return rpcMain(ctx, name, args, stdout, stderr)
	}
}

func rpcMain(ctx context.Context, name string, args []string, stdout, stderr io.Writer) int {
	if slices.Contains(args, "--help") || slices.Contains(args, "-h") {
		fmt.Fprintln(stdout, rpcUsage)
		return 0
	}
	op := ""
	if name == "profile" || name == "config" || name == "script" || name == "fake" || name == "macro" {
		if len(args) == 0 {
			fmt.Fprintln(stderr, rpcUsage)
			return 2
		}
		op, args = args[0], args[1:]
	}
	if name == "devices" && len(args) >= 2 && args[0] == "mapping" {
		op, args = "mapping "+args[1], args[2:]
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("ipc-dir", ipcDiscoveryDir, "daemon discovery directory")
	timeout := fs.Duration("timeout", 10*time.Second, "connection and request timeout")
	compact := fs.Bool("json", false, "compact JSON output")
	var device, id, revision string
	if name == "status" || name == "devices" || name == "identify" || name == "latency" || name == "layer" || name == "profile" && op == "explain" || name == "script" && !slices.Contains([]string{"describe", "init", "import"}, op) {
		fs.StringVar(&device, "device", "", "connection ID or DeviceKey")
	}
	if name == "profile" && op == "explain" || name == "script" && op == "describe" {
		fs.StringVar(&id, "id", "", "library profile instead of live slots")
	}
	if name == "script" && op == "init" {
		fs.StringVar(&id, "id", "", "library profile whose folder receives the script project")
	}
	var replace, force, clearEffects bool
	var resetLatency bool
	if name == "latency" {
		fs.BoolVar(&resetLatency, "reset", false, "forget the samples instead of printing them")
	}
	if name == "fake" && op == "effects" {
		fs.BoolVar(&clearEffects, "clear", false, "forget the calls as they are printed")
	}
	if name == "script" && op == "import" || name == "macro" && op == "import" {
		fs.BoolVar(&replace, "replace", false, "replace an existing script file")
	}
	var newName, replaceID, macros string
	if name == "profile" && op == "export" {
		fs.BoolVar(&force, "force", false, "overwrite FILE if it exists")
	}
	if name == "profile" && op == "import-amgp" {
		fs.StringVar(&newName, "name", "", "name of the new profile instead of the file's")
	}
	if name == "profile" && op == "import" {
		fs.StringVar(&newName, "name", "", "name of the imported profile instead of the archive's")
		fs.StringVar(&replaceID, "replace", "", "existing profile whose folder the archive replaces")
		fs.StringVar(&macros, "macros", "", `"replace" or "keep" the archive's macros whose names the script library has`)
	}
	if op == "put" || name == "profile" && (op == "delete" || op == "restore") {
		fs.StringVar(&revision, "revision", "", "required original revision; empty only when the document is missing")
	}
	var after uint64
	var limit int
	if name == "script" && op == "logs" {
		fs.Uint64Var(&after, "after", 0, "log cursor")
		fs.IntVar(&limit, "limit", 100, "maximum retained log records")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *timeout <= 0 || *timeout > time.Minute {
		fmt.Fprintln(stderr, "padmatectl: timeout must be greater than zero and at most 1m")
		return 2
	}
	args = fs.Args()
	p := map[string]any{}
	if device != "" {
		p["device"] = device
	}
	method, file, scriptFile, archiveFile, exportFile, amgpFile, mappingArg := "", "", "", "", "", "", ""
	needRevision := false
	switch name {
	case "identify":
		if len(args) == 0 {
			method = "devices.identify"
		}
	case "latency":
		if len(args) == 0 {
			method = "latency.get"
			if resetLatency {
				method = "latency.reset"
			}
		}
	case "status", "devices", "pause", "resume":
		switch {
		case name == "devices" && op == "mapping set" && len(args) == 1:
			method, mappingArg = "devices.setMapping", args[0]
		case name == "devices" && op == "mapping reset" && len(args) == 0:
			method = "devices.resetMapping"
		case len(args) == 0 && op == "":
			method = map[string]string{"status": "status.get", "devices": "devices.list", "pause": "engine.pause", "resume": "engine.resume"}[name]
		}
	case "layer":
		if len(args) == 2 && slices.Contains([]string{"switch", "enable", "disable", "toggle"}, args[0]) {
			method, p["op"], p["layer"] = "engine.setLayer", args[0], args[1]
		}
	case "profile":
		switch {
		case op == "list" && len(args) == 0:
			method = "profiles.list"
		case (op == "get" || op == "resolve" || op == "activate") && len(args) == 1:
			method, p["id"] = "profiles."+op, args[0]
		case op == "deactivate" && len(args) == 0:
			method, p["id"] = "profiles.activate", nil
		case op == "put" && len(args) == 2:
			method, p["id"], file, needRevision = "profiles.put", args[0], args[1], true
		case op == "delete" && len(args) == 1:
			method, p["id"], needRevision = "profiles.delete", args[0], true
		case op == "copy" && len(args) == 2:
			method, p["from"], p["to"] = "profiles.copy", args[0], args[1]
		case op == "history" && len(args) == 1:
			method, p["id"] = "profiles.history", args[0]
		case op == "restore" && len(args) == 2:
			method, p["id"], p["version"], needRevision = "profiles.restore", args[0], args[1], true
		case op == "export" && len(args) == 2:
			method, p["id"], exportFile = "profiles.export", args[0], args[1]
		case op == "import" && len(args) == 1 && (newName == "" || replaceID == ""):
			method, archiveFile = "profiles.import", args[0]
			if newName != "" {
				p["name"] = newName
			}
			if replaceID != "" {
				p["replace"] = replaceID
			}
			if flagGiven(fs, "macros") {
				p["macros"] = macros
				if macros != profilelib.MacrosReplace && macros != profilelib.MacrosKeep {
					method = ""
				}
			}
		case op == "import-amgp" && len(args) == 1:
			method, amgpFile = "profiles.importAmgp", args[0]
			if newName != "" {
				p["name"] = newName
			}
		case op == "explain" && len(args) == 1 && (device == "" || id == ""):
			method, p["control"] = "profiles.explain", args[0]
			if id != "" {
				p["id"] = id
			}
		}
	case "config":
		if op == "get" && len(args) == 0 {
			method = "config.get"
		} else if op == "put" && len(args) == 1 {
			method, file, needRevision = "config.put", args[0], true
		}
	case "rpc":
		if len(args) >= 1 && len(args) <= 2 && args[0] != "hello" {
			method = args[0]
			if len(args) == 2 {
				if err := json.Unmarshal([]byte(args[1]), &p); err != nil || p == nil {
					method = ""
				}
			}
		}
	case "script":
		switch {
		case op == "init" && len(args) == 0 && id != "":
			method, p["id"] = "script.init", id
		case op == "import" && len(args) == 2:
			method, p["id"], scriptFile = "script.import", args[0], args[1]
			p["name"], p["replace"] = filepath.Base(args[1]), replace
		}
		if len(args) == 0 && slices.Contains([]string{"status", "registrations", "reload", "describe", "logs"}, op) {
			method = "script." + op
			if op == "describe" && id != "" {
				p["id"] = id
			}
			if op == "logs" {
				if limit < 1 || limit > 256 {
					method = ""
				}
				p["after"], p["limit"] = after, limit
			}
		}
	case "macro":
		switch {
		case op == "list" && len(args) == 0:
			method = "macros.list"
		case op == "new" && len(args) == 1:
			method, p["name"] = "macros.create", args[0]
		case op == "import" && len(args) == 1:
			method, scriptFile = "macros.import", args[0]
			p["name"], p["replace"] = filepath.Base(args[0]), replace
		case op == "rename" && len(args) == 2:
			method, p["file"], p["to"] = "macros.rename", args[0], args[1]
		case op == "delete" && len(args) == 1:
			method, p["file"] = "macros.delete", args[0]
		}
	case "fake":
		switch {
		case (op == "press" || op == "release") && len(args) == 1:
			method, p["control"] = "fake."+op, args[0]
		case op == "axis" && len(args) == 2:
			if value, err := strconv.ParseInt(args[1], 10, 16); err == nil {
				method, p["axis"], p["value"] = "fake.axis", args[0], value
			}
		case op == "effects" && len(args) == 0:
			method = "fake.effects"
			if clearEffects {
				p["clear"] = true
			}
		case op == "battery" && (len(args) == 1 || len(args) == 2):
			if _, ok := input.ParsePowerState(args[0]); !ok {
				break
			}
			method, p["state"] = "fake.battery", args[0]
			if len(args) == 2 {
				if percent, err := strconv.Atoi(args[1]); err == nil && percent >= 0 && percent <= 100 && args[0] != "unknown" {
					p["percent"] = percent
				} else {
					method = ""
				}
			}
		}
	}
	if method == "" {
		fmt.Fprintln(stderr, "padmatectl: invalid daemon command\n"+rpcUsage)
		return 2
	}
	if exportFile != "" && !force {
		if _, err := os.Lstat(exportFile); err == nil {
			fmt.Fprintf(stderr, "padmatectl: %s exists; use --force to overwrite it\n", exportFile)
			return 1
		}
	}
	if archiveFile != "" {
		data, err := readArchiveFile(archiveFile)
		if err != nil {
			fmt.Fprintln(stderr, "padmatectl:", err)
			return 1
		}
		p["data"] = base64.StdEncoding.EncodeToString(data)
	}
	if amgpFile != "" {
		data, err := readAmgpFile(amgpFile)
		if err != nil {
			fmt.Fprintln(stderr, "padmatectl:", err)
			return 1
		}
		p["data"] = base64.StdEncoding.EncodeToString(data)
		p["fileName"] = filepath.Base(amgpFile)
	}
	if mappingArg != "" {
		text, err := mappingText(mappingArg)
		if err != nil {
			fmt.Fprintln(stderr, "padmatectl:", err)
			return 1
		}
		p["mapping"] = text
	}
	if scriptFile != "" {
		data, err := readScriptFile(scriptFile)
		if err != nil {
			fmt.Fprintln(stderr, "padmatectl:", err)
			return 1
		}
		p["text"] = string(data)
	}
	if needRevision {
		if !flagGiven(fs, "revision") {
			fmt.Fprintln(stderr, "padmatectl: --revision is required (--revision= only when the document is missing)")
			return 2
		}
		p["revision"] = revision
		if file != "" {
			data, err := readRPCDocument(file)
			if err != nil {
				fmt.Fprintln(stderr, "padmatectl:", err)
				return 1
			}
			p["text"] = string(data)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if (name == "identify" || strings.HasPrefix(op, "mapping ")) && device == "" {
		id, code := soleDevice(ctx, *dir, stderr)
		if code != 0 {
			return code
		}
		p["device"] = id
	}
	// An export's result holds the archive, which goes to its file; stdout
	// gets a summary instead.
	out, compactOut := stdout, *compact
	var exported bytes.Buffer
	if exportFile != "" {
		out, compactOut = &exported, true
	}
	// An import of an AntiMicroX file prints its report as text, unless --json.
	var imported bytes.Buffer
	if amgpFile != "" && !*compact {
		out, compactOut = &imported, true
	}
	err := rpcInvoke(ctx, *dir, method, p, out, compactOut)
	if err == nil && amgpFile != "" && !*compact {
		err = printAmgpImport(imported.Bytes(), stdout)
	}
	if err == nil && exportFile != "" {
		err = writeExport(exported.Bytes(), p["id"].(string), exportFile, force, stdout, *compact)
	}
	if err != nil {
		fmt.Fprintln(stderr, "padmatectl:", err)
		var rpcErr *jrpc2.Error
		if errors.As(err, &rpcErr) && len(rpcErr.Data) > 0 {
			fmt.Fprintln(stderr, string(rpcErr.Data))
		}
		return 1
	}
	return 0
}

// maxMappingFile bounds the file of "devices mapping set @FILE".
const maxMappingFile = 8 << 10

// mappingText returns the mapping string of the argument of "devices mapping
// set": the argument itself, or for @FILE the text of that file, with the
// space around it taken off.
func mappingText(arg string) (string, error) {
	if !strings.HasPrefix(arg, "@") {
		return arg, nil
	}
	f, err := os.Open(arg[1:])
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxMappingFile+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxMappingFile {
		return "", fmt.Errorf("%s is longer than %d KiB", arg[1:], maxMappingFile>>10)
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("%s is not UTF-8", arg[1:])
	}
	text := strings.TrimSpace(string(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})))
	if text == "" {
		return "", fmt.Errorf("%s is empty", arg[1:])
	}
	return text, nil
}

// deviceRow is the part of a devices.list row that soleDevice reads.
type deviceRow struct {
	Info struct {
		ID uint32 `json:"id"`
	} `json:"info"`
}

// soleDevice returns the connection ID of the one controller the daemon
// lists, for a command that needs one (identify without --device), or the
// exit code when there is none or several, after saying why on stderr.
func soleDevice(ctx context.Context, dir string, stderr io.Writer) (string, int) {
	var list bytes.Buffer
	if err := rpcInvoke(ctx, dir, "devices.list", map[string]any{}, &list, true); err != nil {
		fmt.Fprintln(stderr, "padmatectl:", err)
		return "", 1
	}
	var rows []deviceRow
	if err := json.Unmarshal(list.Bytes(), &rows); err != nil || slices.ContainsFunc(rows, func(r deviceRow) bool { return r.Info.ID == 0 }) {
		fmt.Fprintln(stderr, "padmatectl: unexpected devices.list result")
		return "", 1
	}
	switch len(rows) {
	case 0:
		fmt.Fprintln(stderr, "padmatectl: no controller is connected")
		return "", 1
	case 1:
		return strconv.FormatUint(uint64(rows[0].Info.ID), 10), 0
	}
	fmt.Fprintln(stderr, "padmatectl: several controllers are connected; choose one with --device ID (padmatectl devices lists them)")
	return "", 2
}

// readArchiveFile reads an archive for profile import, at most
// profilelib.MaxArchiveBytes; the daemon checks its contents.
func readArchiveFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, profilelib.MaxArchiveBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > profilelib.MaxArchiveBytes {
		return nil, fmt.Errorf("archive must be at most %d MiB", profilelib.MaxArchiveBytes>>20)
	}
	return data, nil
}

// writeExport writes the archive in result, profiles.export's result for
// profile id, to path, creating it unless force, and prints
// {id,name,file,bytes}.
func writeExport(result []byte, id, path string, force bool, stdout io.Writer, compact bool) error {
	var exported struct {
		Name string `json:"name"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(result, &exported); err != nil {
		return fmt.Errorf("unexpected export result: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(exported.Data)
	if err != nil {
		return fmt.Errorf("unexpected export result: %w", err)
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o666)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s exists; use --force to overwrite it", path)
	}
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if !compact {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(map[string]any{"id": id, "name": exported.Name, "file": path, "bytes": len(data)})
}

func readRPCDocument(path string) ([]byte, error) {
	return readUTF8File(path, profilelib.MaxDocumentBytes, "document must be UTF-8 and at most 4 MiB")
}

func readScriptFile(path string) ([]byte, error) {
	return readUTF8File(path, profilelib.MaxScriptBytes, "script must be UTF-8 and at most 1 MiB")
}

// readUTF8File reads at most limit bytes of UTF-8; problem describes the
// rule a larger or invalid file breaks.
func readUTF8File(path string, limit int, problem string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit || !utf8.Valid(data) {
		return nil, errors.New(problem)
	}
	return data, nil
}

func invokeRPC(ctx context.Context, dir, method string, params any, out io.Writer, compact bool) error {
	conn, d, err := ipc.Dial(ctx, dir)
	if err != nil {
		return err
	}
	client, err := ipc.NewClient(ctx, conn, d, "padmatectl", nil)
	if err != nil {
		return err
	}
	defer client.Close()
	var result json.RawMessage
	if err := client.CallResult(ctx, method, params, &result); err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	if !compact {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(result)
}
