//go:build !windows

package instance

// Lease is a no-op outside Windows, where there is no native live backend.
type Lease struct{}

// Acquire is a no-op outside Windows, preserving cross-platform tooling.
func Acquire(_ string) (*Lease, error) { return &Lease{}, nil }

// Close does nothing outside Windows.
func (*Lease) Close() error { return nil }
