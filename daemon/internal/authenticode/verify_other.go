//go:build !windows

package authenticode

// Verify is only available on Windows.
func Verify(string) (Verdict, error) { return Other, ErrUnsupported }
