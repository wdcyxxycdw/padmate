package spike

import (
	"bytes"
	"fmt"
	"runtime"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

// ActivationReport is the esbuild build and activation measurement.
type ActivationReport struct {
	Runs int
	// First is the first build and activation of the measurement: in
	// spk03 the first esbuild and goja use in the process.
	First ActivationRun
	// Cold is Build + CompileBundle + Activate; Build, Compile and Run
	// are its parts, and CompileRun is Compile + Run: an activation from a
	// cached bundle.
	Cold, Build, Compile, Run, CompileRun Latency
	// Warm is Activate from a cached *goja.Program.
	Warm Latency
	// ColdGCs and WarmGCs are the GC cycles during each pass.
	ColdGCs, WarmGCs uint32
	// WarmAllocs and WarmBytes are the heap allocations of one warm
	// activation, from a pass of their own.
	WarmAllocs, WarmBytes float64

	// Size is the bundle size, inline source map included; SourceMap is
	// the size of the source map comment.
	Size, SourceMap int
	// Registrations is the number of on() calls the script makes.
	Registrations int
	// Program is the compiled bundle, for MeasureDispatch.
	Program *goja.Program
}

// ActivationRun is one cold activation, by part.
type ActivationRun struct {
	Build, Compile, Run int64
}

// MeasureActivation builds and activates s cold runs times, then
// activates the cached program warm runs times.
func MeasureActivation(s *Script, runs int) (*ActivationReport, error) {
	rep := &ActivationReport{Runs: runs}
	var err error
	if rep.First, rep.Program, err = coldActivation(s, rep); err != nil {
		return nil, err
	}

	buildT, compileT, runT := make([]int64, runs), make([]int64, runs), make([]int64, runs)
	coldT, compileRunT := make([]int64, runs), make([]int64, runs)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := range runs {
		r, _, err := coldActivation(s, nil)
		if err != nil {
			return nil, err
		}
		buildT[i], compileT[i], runT[i] = r.Build, r.Compile, r.Run
		coldT[i], compileRunT[i] = r.Build+r.Compile+r.Run, r.Compile+r.Run
	}
	runtime.ReadMemStats(&after)
	rep.ColdGCs = after.NumGC - before.NumGC
	rep.Cold, rep.Build, rep.Compile, rep.Run = latencyOf(coldT), latencyOf(buildT), latencyOf(compileT), latencyOf(runT)
	rep.CompileRun = latencyOf(compileRunT)

	warm := make([]int64, runs)
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := range runs {
		t0 := stamp()
		if _, err := Activate(rep.Program); err != nil {
			return nil, err
		}
		warm[i] = stamp() - t0
	}
	runtime.ReadMemStats(&after)
	rep.WarmGCs = after.NumGC - before.NumGC
	rep.Warm = latencyOf(warm)

	allocRuns := min(runs, 50)
	var allocs, allocBytes uint64
	for range allocRuns {
		runtime.ReadMemStats(&before)
		_, err := Activate(rep.Program)
		runtime.ReadMemStats(&after)
		if err != nil {
			return nil, err
		}
		allocs += after.Mallocs - before.Mallocs
		allocBytes += after.TotalAlloc - before.TotalAlloc
	}
	if allocRuns > 0 {
		rep.WarmAllocs = float64(allocs) / float64(allocRuns)
		rep.WarmBytes = float64(allocBytes) / float64(allocRuns)
	}
	return rep, nil
}

// coldActivation builds, compiles and activates s once. With rep it also
// records the bundle's sizes and registrations.
func coldActivation(s *Script, rep *ActivationReport) (ActivationRun, *goja.Program, error) {
	var r ActivationRun
	t0 := stamp()
	res, err := s.Build()
	t1 := stamp()
	if err != nil {
		return r, nil, err
	}
	p, err := CompileBundle(res)
	t2 := stamp()
	if err != nil {
		return r, nil, fmt.Errorf("spike: compile bundle: %w", err)
	}
	h, err := Activate(p)
	t3 := stamp()
	if err != nil {
		return r, nil, fmt.Errorf("spike: activate: %w", err)
	}
	r = ActivationRun{Build: t1 - t0, Compile: t2 - t1, Run: t3 - t2}
	if rep != nil {
		rep.Size = res.Size
		if i := bytes.LastIndex(res.JS, []byte("//# sourceMappingURL=")); i >= 0 {
			rep.SourceMap = len(res.JS) - i
		}
		rep.Registrations = len(h.Registrations)
	}
	return r, p, nil
}

// TLADiagnostic builds a script with a top-level await and returns the
// diagnostic compile.Build reports.
func TLADiagnostic(s *Script) (string, error) {
	return buildDiagnostic(s, "tla.ts", "import { sleep } from \"padmate\";\nawait sleep(1);\n")
}

func buildDiagnostic(s *Script, name, src string) (string, error) {
	if err := writeFile(s.Root, name, src); err != nil {
		return "", err
	}
	_, err := compile.Build(compile.BuildOptions{Root: s.Root, Entry: name})
	if err == nil {
		return "", fmt.Errorf("spike: %s built without errors", name)
	}
	return err.Error(), nil
}
