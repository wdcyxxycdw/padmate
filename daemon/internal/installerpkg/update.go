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

// UpdateURL is the address the application reads its updates from (ADR-0078):
// the download site, where latest.yml, the installers and their .sha256 files
// sit at the root of the bucket. It ends with a slash, because electron-updater
// resolves the relative names in latest.yml against it. It is written three
// times and a test pins them equal: here, as UPDATE_URL in app/src/main/
// update-config.ts, and as the url of the publish block of app/
// electron-builder.yml (which becomes resources\app-update.yml).
const UpdateURL = "https://padmate.download/"

// UpdateChannel is the channel of the update source: electron-updater asks for
// latest.yml. It is explicit everywhere (UPDATE_CHANNEL, the publish block,
// resources\app-update.yml) because electron-builder takes the channel of a
// prerelease build from its version for any provider but GitHub, so a
// v1.0.3-rc.1 build would write rc.yml and `channel: rc`, which the download
// site does not hold.
const UpdateChannel = "latest"

// formerUpdateSource is the provider of the builds up to v1.0.2, which read
// the releases of the repository on GitHub (ADR-0073).
const formerUpdateSource = "github"

// updateConfigValues reads the keys that start a line of resources\app-update.yml:
// a flat `key: value` file, with the list of publisherName below its key.
func updateConfigValues(data []byte) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimRight(line, "\r"), ":")
		// A key starts the line: the entries of a list do not.
		if !ok || key == "" || key[0] == ' ' || key[0] == '\t' || key[0] == '-' || key[0] == '#' {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values
}

// downloadSiteProblems checks the update source of this source tree: the
// generic provider at UpdateURL with the channel UpdateChannel.
func downloadSiteProblems(values map[string]string) []string {
	var problems []string
	if values["provider"] != "generic" {
		problems = append(problems, fmt.Sprintf("provider is %q, want generic (the download site, ADR-0078)", values["provider"]))
	}
	if got := values["url"]; got != UpdateURL {
		problems = append(problems, fmt.Sprintf("url is %q, want %q", got, UpdateURL))
	}
	if got := values["channel"]; got != UpdateChannel {
		problems = append(problems, fmt.Sprintf("channel is %q, want %q (without it electron-builder takes the channel of a prerelease build from its version)", got, UpdateChannel))
	}
	return problems
}

// formerSourceProblems checks the update source of a build up to v1.0.2:
// GitHub with an owner and a repository.
func formerSourceProblems(values map[string]string) []string {
	var problems []string
	for _, key := range []string{"owner", "repo"} {
		if values[key] == "" {
			problems = append(problems, key+" is missing")
		}
	}
	return problems
}

// cacheDirProblems checks the folder electron-updater downloads to, which the
// installer deletes with a full uninstall.
func cacheDirProblems(values map[string]string) []string {
	if got := values["updaterCacheDirName"]; got != UpdaterCacheDir {
		return []string{fmt.Sprintf("updaterCacheDirName is %q, want %q (the folder build/installer.nsh deletes)", got, UpdaterCacheDir)}
	}
	return nil
}

// CheckUpdateConfig checks the text of resources\app-update.yml, which
// electron-builder writes from the publish block of electron-builder.yml: the
// source is the download site (the generic provider, UpdateURL, UpdateChannel),
// and the cache folder is the one the installer deletes. publisherName is there
// only in a signed build (PKG-09) and is not required here.
func CheckUpdateConfig(data []byte) []string {
	values := updateConfigValues(data)
	return append(downloadSiteProblems(values), cacheDirProblems(values)...)
}

// CheckEarlierUpdateConfig is CheckUpdateConfig for a tree of an earlier build
// (TreeOptions.EarlierBuild), which may still have the source of its time: the
// builds up to v1.0.2 read GitHub. The first installer (INSTALLER) of the
// migration test (`task test:installer INSTALLER=<v1.0.2 installer>
// UPGRADE=<new>`) is such a build.
// Anything else is held to the rule of the current build.
func CheckEarlierUpdateConfig(data []byte) []string {
	values := updateConfigValues(data)
	if values["provider"] == formerUpdateSource {
		return append(formerSourceProblems(values), cacheDirProblems(values)...)
	}
	return append(downloadSiteProblems(values), cacheDirProblems(values)...)
}

// updateConfigProblems reads resources\app-update.yml of the tree at root;
// earlier is TreeOptions.EarlierBuild.
func updateConfigProblems(file string, earlier bool) []string {
	data, err := os.ReadFile(file)
	if err != nil {
		return []string{fmt.Sprintf("resources/app-update.yml: %v", err)}
	}
	check := CheckUpdateConfig
	if earlier {
		check = CheckEarlierUpdateConfig
	}
	var problems []string
	for _, p := range check(data) {
		problems = append(problems, "resources/app-update.yml: "+p)
	}
	return problems
}
