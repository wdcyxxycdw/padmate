package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// The UI's development daemon is isolated by linker flags (instanceName,
// ipcPipe, ipcDir). A release build has none, and the packaged UI's test
// mode (PADMATE_UI_TEST_PACKAGED, app/src/main/runtime-options.ts) and the
// installer smoke test (daemon/internal/installersmoke) must run that very
// build without meeting a padmated of the user. Two environment variables
// give it the same isolation:
//
//	PADMATE_PIPE     the pipe of this instance (ipc.Listen already reads it);
//	                 also makes the instance mutex follow the pipe, and makes
//	                 this a development build for the login startup entry,
//	                 which it never writes (UI-19).
//	PADMATE_IPC_DIR  where it publishes daemon.json, in place of
//	                 %LOCALAPPDATA%\padmate\run. Without it, a private instance
//	                 uses a folder of its own below the temporary folder, never
//	                 the default one, which is the user's own padmated's.
//
// The user's own padmated has neither, so nothing changes for it.
func init() {
	instanceName, ipcDir = privateInstance(instanceName, ipcPipe, ipcDir, os.Getenv("PADMATE_PIPE"), os.Getenv("PADMATE_IPC_DIR"), os.TempDir())
}

// privateInstance returns the instance name and discovery directory of a
// padmated that the linker did not isolate (instance, linkedPipe and
// linkedDir are its flags) and the environment names a pipe (and maybe a
// directory) for. A build with a linked instance or pipe is isolated already
// and keeps its flags. The mutex name hashes the lower-cased pipe, as Windows
// compares pipe names without case. A private instance with no directory gets
// one below tempDir that follows the same hash, so that it cannot overwrite
// the daemon.json of the user's own padmated.
func privateInstance(instance, linkedPipe, linkedDir, envPipe, envDir, tempDir string) (name, dir string) {
	if instance != "" || linkedPipe != "" || envPipe == "" {
		return instance, linkedDir
	}
	sum := sha256.Sum256([]byte(strings.ToLower(envPipe)))
	id := hex.EncodeToString(sum[:8])
	dir = linkedDir
	if dir == "" {
		dir = envDir
	}
	if dir == "" {
		dir = filepath.Join(tempDir, "padmate-private", id)
	}
	return `Local\padmate.instance.pipe.` + id, dir
}
