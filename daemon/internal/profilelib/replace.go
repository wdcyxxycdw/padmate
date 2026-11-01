package profilelib

import (
	"os"
	"time"
)

// The waits of replace between attempts: doubling from the first to the last,
// about 0.6 s in all before it gives up.
const (
	replaceFirstWait = 5 * time.Millisecond
	replaceLastWait  = 320 * time.Millisecond
)

// replace renames from over to. On Windows a file that is open for reading
// cannot be replaced: the rename fails with a sharing violation for as long
// as the reader has it, and readers are many and quick (the daemon serving
// profiles.get, its own reload after a save, a search indexer). Such a failure
// is tried again for a while; any other is returned at once (PITFALLS).
func replace(root *os.Root, from, to string) error {
	err := root.Rename(from, to)
	for wait := replaceFirstWait; err != nil && fileInUse(err) && wait <= replaceLastWait; wait *= 2 {
		time.Sleep(wait)
		err = root.Rename(from, to)
	}
	return err
}
