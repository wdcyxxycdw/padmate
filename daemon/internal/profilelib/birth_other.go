//go:build !windows

package profilelib

import (
	"io/fs"
	"os"
	"time"
)

// birthTime has no answer off Windows: Go's FileInfo carries no creation
// time there, and the modification time moves with every edit (and, for a
// folder, with every save inside it), so it would not be a creation order.
// Clients then fall back to the name.
func birthTime(fs.FileInfo) (created time.Time, ok bool) { return time.Time{}, false }

// setBirthTime does nothing off Windows.
func setBirthTime(*os.File, time.Time) error { return nil }

// setFolderBirthTime does nothing off Windows.
func setFolderBirthTime(*os.Root, string, time.Time) error { return nil }
