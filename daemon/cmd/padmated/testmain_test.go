package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/emergency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/instance"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

// errRealEvent is what Listen returns in the tests for the real event of
// padmatectl release-all.
var errRealEvent = errors.New("test: listening on the real release-all event")

// harnessExit is the exit code of a start that failed in TestM10UIHarness,
// which TestMain exits with after its checks, as padmated would (UI-45).
var harnessExit int

// TestMain keeps the tests off the real event of padmatectl release-all
// (DMN-20): the event is auto-reset, so a test listening on it could take
// a release-all meant for a padmated that runs. live() uses emergencyEvent,
// which is a name of the test process's own here, and testConfig gives
// each test a name of its own. A Listen on emergency.Name fails, and fails
// the package after the tests. The live instance lease likewise uses a
// random name; acquiring the production name fails this package.
func TestMain(m *testing.M) {
	emergencyEvent = fmt.Sprintf(`Local\padmate.test-%d`, os.Getpid())
	instanceName = `Local\padmate.instance-live-test-` + rand.Text()
	ipcPipe = `\\.\pipe\padmate-live-test-` + rand.Text()
	var err error
	ipcDir, err = os.MkdirTemp("", "padmate-live-ipc-")
	if err != nil {
		panic(err)
	}
	var onRealIPC atomic.Int64
	listenPipe := listenIPC
	listenIPC = func(pipe, dir string) (*ipc.Endpoint, error) {
		if pipe == "" || dir == "" {
			onRealIPC.Add(1)
			return nil, errors.New("test: listening on real IPC endpoint")
		}
		return listenPipe(pipe, dir)
	}
	var onRealInstance atomic.Int64
	acquire := acquireInstance
	acquireInstance = func(name string) (*instance.Lease, error) {
		if name == "" {
			onRealInstance.Add(1)
			return nil, errors.New("test: acquiring the real instance lease")
		}
		return acquire(name)
	}
	var onReal atomic.Int64
	listen := listenEmergency
	listenEmergency = func(name string) (*emergency.Listener, error) {
		if name == emergency.Name {
			onReal.Add(1)
			return nil, errRealEvent
		}
		return listen(name)
	}
	code := m.Run()
	if err := os.Remove(ipcDir); err != nil {
		fmt.Fprintln(os.Stderr, "padmated tests: IPC directory cleanup:", err)
		code = 1
	}
	if n := onRealIPC.Load(); n > 0 {
		fmt.Fprintf(os.Stderr, "padmated tests: %d real IPC endpoint attempts\n", n)
		code = 1
	}
	if n := onRealInstance.Load(); n > 0 {
		fmt.Fprintf(os.Stderr, "padmated tests: %d acquisitions of the real instance lease\n", n)
		code = 1
	}
	if n := onReal.Load(); n > 0 {
		fmt.Fprintf(os.Stderr, "padmated tests: %d Listen calls on %s; give live mode liveConfig.emergencyName\n", n, emergency.Name)
		code = 1
	}
	if code == 0 && harnessExit != 0 {
		code = harnessExit
	}
	os.Exit(code)
}
