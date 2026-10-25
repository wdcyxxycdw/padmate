package spike

import (
	"runtime"
	"testing"
)

// BenchmarkDispatch times whole scenario iterations, the event object
// included. For the async scenarios that is the call and the resume, so
// only async-call runs, as "async"; spk03 times the two apart.
func BenchmarkDispatch(b *testing.B) {
	p := program(b)
	for _, s := range Scenarios {
		name := s.Name
		switch name {
		case "async-resume":
			continue
		case "async-call":
			name = "async"
		}
		b.Run(name, func(b *testing.B) {
			runtime.LockOSThread() // for tap+watchdog's thread clock
			defer runtime.UnlockOSThread()
			h, steps, cleanup, err := s.prepare(p)
			if err != nil {
				b.Fatal(err)
			}
			defer func() {
				if err := cleanup(); err != nil {
					b.Error(err)
				}
			}()
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				for _, st := range steps {
					if err := st.run(i); err != nil {
						b.Fatal(err)
					}
				}
				h.Reset()
				i++
			}
		})
	}
}

// BenchmarkActivateWarm is an activation from a cached program: a new
// runtime, the host global and the bundle's top-level code.
func BenchmarkActivateWarm(b *testing.B) {
	p := program(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Activate(p); err != nil {
			b.Fatal(err)
		}
	}
}
