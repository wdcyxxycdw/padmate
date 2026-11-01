//go:build !windows

package output

// AsyncKeyDown always returns false off Windows, where there is no
// GetAsyncKeyState: the startup sweep of the held-key log then
// releases nothing unless LoopOptions.KeyDown says otherwise.
func AsyncKeyDown(KeyEvent) bool { return false }
