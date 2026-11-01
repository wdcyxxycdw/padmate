package profilelib

import (
	"errors"
	"testing"
)

// TestFileInUseOnlyForSharing: errors that are not about another handle are
// returned at once, everywhere.
func TestFileInUseOnlyForSharing(t *testing.T) {
	for _, err := range []error{nil, errors.New("disk full"), errors.ErrUnsupported} {
		if fileInUse(err) {
			t.Errorf("fileInUse(%v) = true", err)
		}
	}
}
