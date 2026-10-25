package installerpkg

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// The parts of an installation that the automatic update (PKG-10, ADR-0073)
// depends on, and that CheckTree reads: the main process of the window, which
// must be one file that needs nothing installed, and resources\app-update.yml,
// which electron-builder writes for the update source.

// UpdaterCacheDir is the folder of %LOCALAPPDATA% where electron-updater keeps
// what it downloads and where electron-builder's installer keeps a copy of
// itself. build/installer.nsh (PADMATE_UPDATER_DIR) and app/src/main/
// update-config.ts name it too; a full uninstall deletes it.
const UpdaterCacheDir = "padmate-updater"

// MainBundle is the main process of the window inside app.asar.
const MainBundle = "out/main/index.js"

// nodeModules are the names Node's module.builtinModules lists (Node 24):
// what the main process may require besides electron. The installed
// application has no node_modules.
var nodeModules = map[string]bool{
	"_http_agent": true, "_http_client": true, "_http_common": true, "_http_incoming": true,
	"_http_outgoing": true, "_http_server": true, "_stream_duplex": true, "_stream_passthrough": true,
	"_stream_readable": true, "_stream_transform": true, "_stream_wrap": true, "_stream_writable": true,
	"_tls_common": true, "_tls_wrap": true, "assert": true, "assert/strict": true, "async_hooks": true,
	"buffer": true, "child_process": true, "cluster": true, "console": true, "constants": true,
	"crypto": true, "dgram": true, "diagnostics_channel": true, "dns": true, "dns/promises": true,
	"domain": true, "events": true, "fs": true, "fs/promises": true, "http": true, "http2": true,
	"https": true, "inspector": true, "inspector/promises": true, "module": true, "net": true,
	"os": true, "path": true, "path/posix": true, "path/win32": true, "perf_hooks": true,
	"process": true, "punycode": true, "querystring": true, "readline": true, "readline/promises": true,
	"repl": true, "stream": true, "stream/consumers": true, "stream/promises": true, "stream/web": true,
	"string_decoder": true, "sys": true, "timers": true, "timers/promises": true, "tls": true,
	"trace_events": true, "tty": true, "url": true, "util": true, "util/types": true, "v8": true,
	"vm": true, "wasi": true, "worker_threads": true, "zlib": true,
	// Only with the node: prefix.
	"node:sea": true, "node:sqlite": true, "node:test": true, "node:test/reporters": true,
}

// isNodeModule reports whether name is one of Node's own modules, with or
// without the node: prefix.
func isNodeModule(name string) bool {
	if nodeModules[name] {
		return true
	}
	rest, ok := strings.CutPrefix(name, "node:")
	return ok && nodeModules[rest]
}

var requireCall = regexp.MustCompile(`(?:^|[^\w$.])require\(\s*([^)]*?)\s*\)`)

// MainBundleRequires lists the modules that JavaScript code requires and an
// installed application cannot find: anything that is not electron or one of
// Node's own, each once, in order of appearance. "<dynamic>" stands for a
// require whose argument is not a plain string. app/scripts/
// main-bundle-guard.mjs applies the same rule to the bundle at build time.
func MainBundleRequires(code []byte) []string {
	var found []string
	add := func(name string) {
		for _, f := range found {
			if f == name {
				return
			}
		}
		found = append(found, name)
	}
	for _, m := range requireCall.FindAllSubmatch(code, -1) {
		arg := string(m[1])
		if len(arg) < 2 || !strings.ContainsRune("\"'`", rune(arg[0])) || arg[len(arg)-1] != arg[0] ||
			strings.ContainsAny(arg[1:len(arg)-1], "\"'`") {
			add("<dynamic>")
			continue
		}
		name := arg[1 : len(arg)-1]
		if name != "electron" && !isNodeModule(name) {
			add(name)
		}
	}
	return found
}

// mainBundleProblems turns what was read of the bundle into problems.
func mainBundleProblems(bundle []byte, err error) []string {
	if err != nil {
		return []string{fmt.Sprintf("resources/app.asar: %v", err)}
	}
	bare := MainBundleRequires(bundle)
	if len(bare) == 0 {
		return nil
	}
	quoted := make([]string, len(bare))
	for i, name := range bare {
		quoted[i] = fmt.Sprintf("%q", name)
	}
	return []string{fmt.Sprintf("resources/app.asar: %s requires %s, which the installed application does not have (it has no node_modules); bundle it in electron.vite.config.ts", MainBundle, strings.Join(quoted, ", "))}
}

// CheckUpdateConfig checks the text of resources\app-update.yml, which
// electron-builder writes from the publish block of electron-builder.yml: the
// source is GitHub with an owner and a repository, and the cache folder is
// the one the installer deletes. publisherName is there only in a signed
// build (PKG-09) and is not required here.
func CheckUpdateConfig(data []byte) []string {
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimRight(line, "\r"), ":")
		// A key starts the line: the entries of a list do not.
		if !ok || key == "" || key[0] == ' ' || key[0] == '\t' || key[0] == '-' || key[0] == '#' {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	var problems []string
	if values["provider"] != "github" {
		problems = append(problems, fmt.Sprintf("provider is %q, want github", values["provider"]))
	}
	for _, key := range []string{"owner", "repo"} {
		if values[key] == "" {
			problems = append(problems, key+" is missing")
		}
	}
	if got := values["updaterCacheDirName"]; got != UpdaterCacheDir {
		problems = append(problems, fmt.Sprintf("updaterCacheDirName is %q, want %q (the folder build/installer.nsh deletes)", got, UpdaterCacheDir))
	}
	return problems
}

// updateConfigProblems reads resources\app-update.yml of the tree at root.
func updateConfigProblems(file string) []string {
	data, err := os.ReadFile(file)
	if err != nil {
		return []string{fmt.Sprintf("resources/app-update.yml: %v", err)}
	}
	var problems []string
	for _, p := range CheckUpdateConfig(data) {
		problems = append(problems, "resources/app-update.yml: "+p)
	}
	return problems
}
