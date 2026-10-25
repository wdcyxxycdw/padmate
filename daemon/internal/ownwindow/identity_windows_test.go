package ownwindow

import (
	"os"
	"testing"
)

func TestProcessInstanceCannotBeInheritedByReusedPID(t *testing.T) {
	pid := uint32(os.Getpid())
	created, err := Instance(pid)
	if err != nil {
		t.Fatal(err)
	}
	path, err := image(pid)
	if err != nil {
		t.Fatal(err)
	}
	if created == 0 || !Alive(pid, path, created) {
		t.Fatal("current process instance not recognized")
	}
	if Alive(pid, path, created+1) {
		t.Fatal("PID identity accepted wrong process creation time")
	}
	if _, err := Verify(pid, pid); err == nil {
		t.Fatal("bridge accepted a process that is not its own child")
	}
}
