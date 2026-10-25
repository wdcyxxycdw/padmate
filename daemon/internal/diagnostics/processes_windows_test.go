package diagnostics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessEnumerationIncludesSelf(t *testing.T) {
	names, err := Processes()
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if strings.EqualFold(name, filepath.Base(self)) {
			return
		}
	}
	t.Fatal("process enumeration omitted this test process")
}
