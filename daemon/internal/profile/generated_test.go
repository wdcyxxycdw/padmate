package profile

import (
	"os/exec"
	"testing"
)

func TestGeneratedSchemaTypesCurrent(t *testing.T) {
	cmd := exec.Command("go", "run", "../../tools/codegen", "-root", "../../..", "-check")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("schema type drift: %v\n%s", err, out)
	}
}
