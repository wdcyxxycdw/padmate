//go:build !windows

package output

// SystemHeldIdentity returns the zero HeldIdentity off Windows, where
// padmated does not inject: the held-key logs of the tests there are all
// of one logon session unless LoopOptions.Identity says
// otherwise.
func SystemHeldIdentity() (HeldIdentity, error) { return HeldIdentity{}, nil }
