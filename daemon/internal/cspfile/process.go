package cspfile

import "strings"

// IsCSPImage reports whether a process image name is CSP's executable. The
// match is case-insensitive and on the image name only; window titles are
// never read.
func IsCSPImage(name string) bool { return strings.EqualFold(name, ExeName) }
