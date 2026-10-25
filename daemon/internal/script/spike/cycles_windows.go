package spike

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// cpuKey is where Windows describes CPU 0 at boot. It is only read.
const cpuKey = `HARDWARE\DESCRIPTION\System\CentralProcessor\0`

// workUnit is the number of dependent multiply-adds in one spin
// iteration.
const workUnit = 64

// sink keeps the spin loop's arithmetic from being optimized away.
var sink atomic.Uint64

// CycleCalibration runs the measurements opts selects on a new OS thread
// and returns them. The thread stays locked until its goroutine exits, so
// the Go runtime destroys it afterwards and the affinity the per-CPU
// spins give it can never reach another goroutine; it is restored anyway.
func CycleCalibration(opts CycleOptions) (*CycleReport, error) {
	type result struct {
		r   *CycleReport
		err error
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread() // never unlocked, see above
		r, err := calibrate(opts)
		done <- result{r, err}
	}()
	res := <-done
	return res.r, res.err
}

func calibrate(opts CycleOptions) (*CycleReport, error) {
	q := qpc{qpcFrequency()}
	r := &CycleReport{
		QPCFrequency: q.freq,
		LogicalCPUs:  int(windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS)),
	}
	var err error
	if r.CPU, r.NominalMHz, err = cpuInfo(); err != nil {
		return nil, err
	}
	var minRes, maxRes, curRes uint32
	if err := ntQueryTimerResolution(&minRes, &maxRes, &curRes); err != nil {
		return nil, fmt.Errorf("NtQueryTimerResolution: %w", err)
	}
	r.TimerResolution = time.Duration(curRes) * 100
	if r.QueryCost, err = queryCost(q); err != nil {
		return nil, err
	}
	for _, d := range opts.Spins {
		res := SpinResult{Target: d}
		for range opts.Runs {
			s, err := spin(q, d)
			if err != nil {
				return nil, err
			}
			res.Samples = append(res.Samples, s)
		}
		r.Spins = append(r.Spins, res)
	}
	for range opts.Calibrations {
		c, err := startupCalibration(q)
		if err != nil {
			return nil, err
		}
		r.Calibrations = append(r.Calibrations, c)
	}
	if opts.Sleep > 0 {
		if r.Sleep, err = sleepSpin(q, opts.SleepSpin, opts.Sleep); err != nil {
			return nil, err
		}
	}
	if opts.PerCPU > 0 {
		if r.CPUs, r.Migration, err = perCPU(q, opts.PerCPU); err != nil {
			return nil, err
		}
	}
	if opts.CrossThread > 0 {
		if r.CrossThread, err = crossThread(q, opts.CrossThread, opts.PollEvery); err != nil {
			return nil, err
		}
	}
	if opts.ThreadTimes > 0 {
		if r.ThreadTimes, err = threadTimes(q, opts.ThreadTimes, opts.ThreadTimesLimit); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// qpc reads and converts QueryPerformanceCounter.
type qpc struct{ freq int64 }

func (qpc) now() int64 {
	var n int64
	_ = queryPerformanceCounter(&n) // cannot fail since Windows XP
	return n
}

func (q qpc) duration(ticks int64) time.Duration { return ticksDuration(ticks, q.freq) }

func (q qpc) ticks(d time.Duration) int64 {
	s := int64(time.Second)
	return int64(d)/s*q.freq + int64(d)%s*q.freq/s
}

// cycles reads the cycle time of thread.
func cycles(thread windows.Handle) (uint64, error) {
	var c uint64
	if err := queryThreadCycleTime(thread, &c); err != nil {
		return 0, fmt.Errorf("QueryThreadCycleTime: %w", err)
	}
	return c, nil
}

// spin spins the calling thread for d of QPC time.
func spin(q qpc, d time.Duration) (Sample, error) {
	self := windows.CurrentThread()
	c0, err := cycles(self)
	if err != nil {
		return Sample{}, err
	}
	q0 := q.now()
	s := spinUntil(q, q0+q.ticks(d))
	q1 := q.now()
	c1, err := cycles(self)
	if err != nil {
		return Sample{}, err
	}
	s.Wall, s.Cycles = q.duration(q1-q0), c1-c0
	return s, nil
}

// spinUntil does units of fixed work until QPC reaches end and watches
// the processor it runs on. It fills in Sample except Wall and Cycles.
func spinUntil(q qpc, end int64) Sample {
	x := sink.Load() | 1
	cpu := getCurrentProcessorNumber()
	s := Sample{FirstCPU: cpu}
	for q.now() < end {
		for range workUnit {
			x = x*6364136223846793005 + 1442695040888963407
		}
		s.Iterations++
		if n := getCurrentProcessorNumber(); n != cpu {
			s.Migrations++
			cpu = n
		}
	}
	s.LastCPU = cpu
	sink.Store(x)
	return s
}

// startupCalibration is the calibration proposed for the watchdog: the
// fastest of CalibrationSpins spins of CalibrationSpin, in cycles per µs.
func startupCalibration(q qpc) (float64, error) {
	best := 0.0
	for range CalibrationSpins {
		s, err := spin(q, CalibrationSpin)
		if err != nil {
			return 0, err
		}
		best = max(best, s.CyclesPerMicrosecond())
	}
	return best, nil
}

// queryCost times QueryThreadCycleTime on the calling thread.
func queryCost(q qpc) (time.Duration, error) {
	const n = 10000
	self := windows.CurrentThread()
	var c uint64
	q0 := q.now()
	for range n {
		if err := queryThreadCycleTime(self, &c); err != nil {
			return 0, fmt.Errorf("QueryThreadCycleTime: %w", err)
		}
	}
	return q.duration(q.now()-q0) / n, nil
}

// sleepSpin spins, sleeps with SleepEx and spins again, reading the cycle
// time around each part.
func sleepSpin(q qpc, spinFor, sleep time.Duration) (*SleepResult, error) {
	r := &SleepResult{Requested: sleep}
	var err error
	if r.Before, err = spin(q, spinFor); err != nil {
		return nil, err
	}
	self := windows.CurrentThread()
	c0, err := cycles(self)
	if err != nil {
		return nil, err
	}
	q0 := q.now()
	windows.SleepEx(uint32(sleep/time.Millisecond), false)
	q1 := q.now()
	c1, err := cycles(self)
	if err != nil {
		return nil, err
	}
	r.Slept, r.SleptCycles = q.duration(q1-q0), c1-c0
	if r.After, err = spin(q, spinFor); err != nil {
		return nil, err
	}
	return r, nil
}

// cpuSet is one logical processor of group 0.
type cpuSet struct {
	cpu         uint32
	core, class uint8
}

// cpuSetInformation is CpuSetInformation, the only
// CPU_SET_INFORMATION_TYPE.
const cpuSetInformation = 0

// cpuSets lists the logical processors of group 0 with their efficiency
// class (GetSystemCpuSetInformation).
func cpuSets() ([]cpuSet, error) {
	var n uint32
	err := getSystemCpuSetInformation(nil, 0, &n, 0, 0)
	if err != nil && !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		return nil, fmt.Errorf("GetSystemCpuSetInformation: %w", err)
	}
	if n == 0 {
		return nil, errors.New("GetSystemCpuSetInformation returned no CPU sets")
	}
	buf := make([]byte, n)
	if err := getSystemCpuSetInformation(&buf[0], n, &n, 0, 0); err != nil {
		return nil, fmt.Errorf("GetSystemCpuSetInformation: %w", err)
	}
	return parseCPUSets(buf[:n])
}

// parseCPUSets reads SYSTEM_CPU_SET_INFORMATION entries: Size (offset
// 0), Type (4), then CpuSet.Group (12), LogicalProcessorIndex (14),
// CoreIndex (15) and EfficiencyClass (18). It keeps the processors of
// group 0 that fit in an affinity mask.
func parseCPUSets(buf []byte) ([]cpuSet, error) {
	var out []cpuSet
	for len(buf) >= 8 {
		size := binary.LittleEndian.Uint32(buf)
		if size < 8 || uint64(size) > uint64(len(buf)) {
			return nil, fmt.Errorf("SYSTEM_CPU_SET_INFORMATION entry of %d bytes, %d left", size, len(buf))
		}
		e := buf[:size]
		buf = buf[size:]
		if len(e) < 19 || binary.LittleEndian.Uint32(e[4:]) != cpuSetInformation ||
			binary.LittleEndian.Uint16(e[12:]) != 0 || int(e[14]) >= bits.UintSize {
			continue
		}
		out = append(out, cpuSet{cpu: uint32(e[14]), core: e[15], class: e[18]})
	}
	return out, nil
}

// perCPU spins pinned to each processor in turn, then across a forced
// migration, and restores the thread's affinity.
func perCPU(q qpc, d time.Duration) (_ []CPUResult, _ *MigrationResult, err error) {
	sets, err := cpuSets()
	if err != nil {
		return nil, nil, err
	}
	self := windows.CurrentThread()
	var orig uintptr // the affinity before the first pin
	pin := func(s cpuSet) error {
		prev, err := setThreadAffinityMask(self, 1<<s.cpu)
		if err != nil {
			return fmt.Errorf("SetThreadAffinityMask(CPU %d): %w", s.cpu, err)
		}
		if orig == 0 {
			orig = prev
		}
		return nil
	}
	defer func() {
		if orig == 0 {
			return
		}
		if _, rerr := setThreadAffinityMask(self, orig); rerr != nil && err == nil {
			err = fmt.Errorf("restore the thread affinity: %w", rerr)
		}
	}()

	var results []CPUResult
	var fast, slow *cpuSet
	for i, s := range sets {
		if err := pin(s); err != nil {
			// Outside the process affinity (a job object, say): skip it.
			if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
				continue
			}
			return nil, nil, err
		}
		sample, err := spin(q, d)
		if err != nil {
			return nil, nil, err
		}
		results = append(results, CPUResult{CPU: s.cpu, Core: s.core, EfficiencyClass: s.class, Sample: sample})
		if fast == nil || s.class > fast.class {
			fast = &sets[i]
		}
		if slow == nil || s.class <= slow.class {
			slow = &sets[i]
		}
	}
	if fast == nil || fast == slow {
		return results, nil, nil
	}
	mig, err := migrate(q, d, *fast, *slow, pin)
	return results, mig, err
}

// migrate spins d/2 pinned to from and d/2 pinned to to.
func migrate(q qpc, d time.Duration, from, to cpuSet, pin func(cpuSet) error) (*MigrationResult, error) {
	self := windows.CurrentThread()
	if err := pin(from); err != nil {
		return nil, err
	}
	var c [4]uint64
	var t [4]int64
	var err error
	read := func(i int) {
		if err == nil {
			c[i], err = cycles(self)
			t[i] = q.now()
		}
	}
	read(0)
	first := spinUntil(q, t[0]+q.ticks(d/2))
	read(1)
	if err == nil {
		err = pin(to)
	}
	read(2)
	second := spinUntil(q, t[2]+q.ticks(d/2))
	read(3)
	if err != nil {
		return nil, err
	}
	first.Wall, first.Cycles = q.duration(t[1]-t[0]), c[1]-c[0]
	second.Wall, second.Cycles = q.duration(t[3]-t[2]), c[3]-c[2]
	total := Sample{
		Wall:       q.duration(t[3] - t[0]),
		Cycles:     c[3] - c[0],
		Iterations: first.Iterations + second.Iterations,
		Migrations: first.Migrations + second.Migrations,
		FirstCPU:   first.FirstCPU,
		LastCPU:    second.LastCPU,
	}
	if first.LastCPU != second.FirstCPU {
		total.Migrations++
	}
	return &MigrationResult{
		From:         CPUResult{CPU: from.cpu, Core: from.core, EfficiencyClass: from.class, Sample: first},
		To:           CPUResult{CPU: to.cpu, Core: to.core, EfficiencyClass: to.class, Sample: second},
		Total:        total,
		Switch:       q.duration(t[2] - t[1]),
		SwitchCycles: c[2] - c[1],
	}, nil
}

// crossThread spins on a second locked thread three times: with no
// reader, with a tight reader and with one that reads every `every`.
func crossThread(q qpc, d, every time.Duration) (*CrossThreadResult, error) {
	r := &CrossThreadResult{PollEvery: every}
	var err error
	if r.Alone, err = polledSpin(q, d, noReads, nil); err != nil {
		return nil, err
	}
	if r.Tight, err = polledSpin(q, d, 0, &r.Intervals); err != nil {
		return nil, err
	}
	if r.Periodic, err = polledSpin(q, d, every, nil); err != nil {
		return nil, err
	}
	return r, nil
}

// noReads makes polledSpin wait for the spin without reading.
const noReads time.Duration = -1

// polledSpin spins d on a second locked thread while the calling thread
// reads its cycle time through a real handle: every `every` with
// time.Sleep in between, in a tight loop when every is 0, or not at all
// when it is noReads. When intervals is not nil, it gets the QPC time
// between changes of the value read, from the first change on.
func polledSpin(q qpc, d, every time.Duration, intervals *[]time.Duration) (PolledSpin, error) {
	type opened struct {
		h   windows.Handle
		err error
	}
	type spun struct {
		s   Sample
		err error
	}
	hc := make(chan opened, 1)
	start := make(chan struct{})
	sc := make(chan spun, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		h, err := windows.OpenThread(windows.THREAD_QUERY_LIMITED_INFORMATION, false, windows.GetCurrentThreadId())
		hc <- opened{h, err}
		if err != nil {
			return
		}
		<-start
		s, err := spin(q, d)
		sc <- spun{s, err}
	}()
	o := <-hc
	if o.err != nil {
		return PolledSpin{}, fmt.Errorf("OpenThread: %w", o.err)
	}
	defer func() { _ = windows.CloseHandle(o.h) }()
	last, err := cycles(o.h)
	close(start)
	if err != nil || every == noReads {
		res := <-sc
		if err == nil {
			err = res.err
		}
		return PolledSpin{Spin: res.s}, err
	}

	var p PolledSpin
	var lastChange int64
	phased := false
	begin := q.now()
	for {
		select {
		case res := <-sc:
			if res.err != nil {
				return PolledSpin{}, res.err
			}
			p.Spin = res.s
			if p.Reads > 0 {
				p.PerRead = q.duration(q.now()-begin) / time.Duration(p.Reads)
			}
			return p, nil
		default:
		}
		if every > 0 {
			time.Sleep(every)
		}
		v, err := cycles(o.h)
		if err != nil {
			<-sc
			return PolledSpin{}, err
		}
		p.Reads++
		if intervals == nil || v == last {
			continue
		}
		now := q.now()
		if phased {
			*intervals = append(*intervals, q.duration(now-lastChange))
		}
		phased, last, lastChange = true, v, now
	}
}

// threadTimes polls GetThreadTimes on the calling thread until it has
// seen n increments or limit has passed.
func threadTimes(q qpc, n int, limit time.Duration) (*ThreadTimesResult, error) {
	self := windows.CurrentThread()
	read := func() (time.Duration, error) {
		var created, exited, kernel, user windows.Filetime
		if err := getThreadTimes(self, &created, &exited, &kernel, &user); err != nil {
			return 0, fmt.Errorf("GetThreadTimes: %w", err)
		}
		return filetimeDuration(kernel) + filetimeDuration(user), nil
	}
	last, err := read()
	if err != nil {
		return nil, err
	}
	r := &ThreadTimesResult{}
	deadline := q.now() + q.ticks(limit)
	var lastChange int64
	phased := false
	for len(r.Increments) < n {
		now := q.now()
		if now >= deadline {
			break
		}
		t, err := read()
		if err != nil {
			return nil, err
		}
		if t == last {
			continue
		}
		if phased {
			r.Increments = append(r.Increments, t-last)
			r.Intervals = append(r.Intervals, q.duration(now-lastChange))
		}
		phased, last, lastChange = true, t, now
	}
	return r, nil
}

// filetimeDuration converts a FILETIME that holds a duration, in 100ns
// units, rather than a date.
func filetimeDuration(ft windows.Filetime) time.Duration {
	return time.Duration(int64(ft.HighDateTime)<<32|int64(ft.LowDateTime)) * 100
}

// cpuInfo reads the name and the ~MHz value of CPU 0.
func cpuInfo() (name string, mhz uint32, err error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, cpuKey, registry.QUERY_VALUE)
	if err != nil {
		return "", 0, fmt.Errorf(`open HKLM\%s: %w`, cpuKey, err)
	}
	defer k.Close()
	name, _, err = k.GetStringValue("ProcessorNameString")
	if err != nil {
		return "", 0, fmt.Errorf(`read HKLM\%s\ProcessorNameString: %w`, cpuKey, err)
	}
	v, _, err := k.GetIntegerValue("~MHz")
	if err != nil {
		return "", 0, fmt.Errorf(`read HKLM\%s\~MHz: %w`, cpuKey, err)
	}
	return strings.TrimSpace(name), uint32(v), nil
}
