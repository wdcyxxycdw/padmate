//go:build !windows

package emergency

import "context"

// Listener is unavailable off Windows: Listen never returns one.
type Listener struct{}

// Listen is unavailable off Windows.
func Listen(string) (*Listener, error) { return nil, ErrUnsupported }

// Serve is unavailable off Windows.
func (*Listener) Serve(context.Context, func()) error { return ErrUnsupported }

// Close does nothing off Windows.
func (*Listener) Close() error { return nil }

// Trigger is unavailable off Windows.
func Trigger(string) error { return ErrUnsupported }
