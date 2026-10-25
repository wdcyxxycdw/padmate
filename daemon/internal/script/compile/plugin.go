package compile

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// padmateNamespace is the esbuild namespace of the virtual padmate modules.
const padmateNamespace = "padmate"

// padmateStub is the host bridge for the v0 API. It contains no
// implementation: state belongs to the Go host object installed before a
// bundle runs. Keeping this module virtual prevents imports from escaping the
// profile root.
const padmateStub = `const bridge = globalThis.` + HostGlobal + `;
export const host = bridge.host;
export const defineScript = bridge.defineScript;
export const defineAction = bridge.defineAction;
export const param = bridge.param;
export const on = bridge.on;
export const global = bridge.global;
export const layer = bridge.layer;
export const layers = bridge.layers;
export const claim = bridge.claim;
export const observe = bridge.observe;
export const pad = bridge.pad;
export const key = bridge.key;
export const now = bridge.now;
export const sleep = bridge.sleep;
export const every = bridge.every;
export const log = bridge.log;
export const onAxis = bridge.onAxis;
export const onStick = bridge.onStick;
export const onTick = bridge.onTick;
export const session = bridge.session;
export const app = bridge.app;
export const profiles = bridge.profiles;
export const hud = bridge.hud;
`

// nodeBuiltins are the Node.js 24 built-in modules that have no "node:"
// prefix (require("module").builtinModules, without the internal "_" ones).
// Any "node:" specifier is rejected as well.
var nodeBuiltins = []string{
	"assert", "assert/strict", "async_hooks", "buffer", "child_process", "cluster",
	"console", "constants", "crypto", "dgram", "diagnostics_channel", "dns",
	"dns/promises", "domain", "events", "fs", "fs/promises", "http", "http2",
	"https", "inspector", "inspector/promises", "module", "net", "os", "path",
	"path/posix", "path/win32", "perf_hooks", "process", "punycode",
	"querystring", "readline", "readline/promises", "repl", "stream",
	"stream/consumers", "stream/promises", "stream/web", "string_decoder", "sys",
	"timers", "timers/promises", "tls", "trace_events", "tty", "url", "util",
	"util/types", "v8", "vm", "wasi", "worker_threads", "zlib",
}

func isNodeBuiltin(path string) bool {
	return strings.HasPrefix(path, "node:") || slices.Contains(nodeBuiltins, path)
}

// urlScheme matches a URL scheme. It needs two characters, so a drive
// letter is not one.
var urlScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]+:`)

// isURL reports whether path is a URL, protocol-relative ones included.
// esbuild would mark http:, https: and // imports external and bundle
// data: ones.
func isURL(path string) bool {
	return strings.HasPrefix(path, "//") || urlScheme.MatchString(path)
}

// isAbsolute reports whether path is absolute on any OS: rooted with / or
// \ (UNC and \\?\ paths included) or starting with a drive letter. These
// are rejected before esbuild resolves them, since resolving a UNC path
// opens an SMB connection to its host.
func isAbsolute(path string) bool {
	return strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`) ||
		len(path) >= 2 && path[1] == ':' && ('A' <= path[0] && path[0] <= 'Z' || 'a' <= path[0] && path[0] <= 'z')
}

// resolving marks the plugin's own build.Resolve calls, so the OnResolve
// callback lets esbuild handle them instead of recursing.
type resolving struct{}

// plugin enforces the import rules of SCRIPTING §3 for the script root,
// which must be canonical, and serves the virtual padmate modules, by
// import path.
func plugin(root string, virtualModules map[string]string) api.Plugin {
	return api.Plugin{
		Name: "padmate",
		Setup: func(build api.PluginBuild) {
			build.OnResolve(api.OnResolveOptions{Filter: `.*`}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
				if _, ok := args.PluginData.(resolving); ok || args.Kind == api.ResolveEntryPoint {
					return api.OnResolveResult{}, nil
				}
				switch {
				case args.Path == "padmate" || strings.HasPrefix(args.Path, "padmate/"):
					if _, ok := virtualModules[args.Path]; !ok {
						return fail(fmt.Sprintf("module %q does not exist; the M0d stub only provides \"padmate\"", args.Path)), nil
					}
					return api.OnResolveResult{Path: args.Path, Namespace: padmateNamespace}, nil
				case isNodeBuiltin(args.Path):
					return fail(fmt.Sprintf("Node.js built-in module %q is not available in padmate scripts", args.Path)), nil
				case isURL(args.Path):
					return fail(fmt.Sprintf("%q is a URL; padmate scripts can only import files in the script root", args.Path)), nil
				case isAbsolute(args.Path):
					return fail(fmt.Sprintf("%q is an absolute path; import files in the script root with a relative path", args.Path)), nil
				}
				r := build.Resolve(args.Path, api.ResolveOptions{
					Importer:   args.Importer,
					Namespace:  args.Namespace,
					ResolveDir: args.ResolveDir,
					Kind:       args.Kind,
					With:       args.With,
					PluginData: resolving{},
				})
				switch {
				case len(r.Errors) > 0:
					// Let esbuild resolve it again, so the failure gets its
					// usual message and location.
				case r.External:
					return fail(fmt.Sprintf("%q is external; padmate scripts can only import files in the script root", args.Path)), nil
				case r.Namespace == "file" && !inRoot(root, r.Path):
					return fail(fmt.Sprintf("%q resolves to a file outside the script root", args.Path)), nil
				case r.Namespace == "file" && inHistory(root, r.Path):
					return fail(fmt.Sprintf("%q is in the profile's %s folder of saved versions, not script source", args.Path, profile.HistoryDir)), nil
				}
				return api.OnResolveResult{}, nil
			})
			// The backstop for files that skip OnResolve: esbuild resolves
			// a computed import() or require() path (a template literal or a
			// string concatenation) as a glob, without calling plugins.
			build.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: "file"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				inside := inRoot(root, args.Path)
				if inside && !inHistory(root, args.Path) {
					// No contents: esbuild loads the file as usual.
					return api.OnLoadResult{}, nil
				}
				name := args.Path
				if rel, err := filepath.Rel(root, args.Path); err == nil {
					name = filepath.ToSlash(rel)
				}
				text := fmt.Sprintf("%q is outside the script root; a computed import() or require() path matches it", name)
				if inside {
					text = fmt.Sprintf("%q is in the profile's %s folder of saved versions; a computed import() or require() path matches it", name, profile.HistoryDir)
				}
				return api.OnLoadResult{Errors: []api.Message{{Text: text}}}, nil
			})
			build.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: padmateNamespace}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				contents := virtualModules[args.Path]
				return api.OnLoadResult{Contents: &contents, Loader: api.LoaderJS}, nil
			})
		},
	}
}

// fail is an OnResolve result with one error. esbuild attaches the location
// of the import.
func fail(text string) api.OnResolveResult {
	return api.OnResolveResult{Errors: []api.Message{{Text: text}}}
}
