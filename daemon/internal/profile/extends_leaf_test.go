package profile

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// merger.leaf keeps what a parent that is a merge itself knows about the
// values it copies (PRO-19, ADR-0070). It finds the origins below a value by
// the sorted pointers, not by looking at all of them for every value: a chain
// of large profiles must not take time in the product of the two.
func TestMergerLeafScalesWithTheOriginsBelowIt(t *testing.T) {
	const layers = 8000
	origins := make(map[string]string, 2*layers)
	for i := range layers {
		// l1 must not take the origins of l10, l11, ...: the pointer of a
		// value ends at its "/".
		origins[fmt.Sprintf("/layers/l%d/south", i)] = "grand"
		origins[fmt.Sprintf("/layers/l%d/east", i)] = "parent"
	}
	parent := &doc{id: "parent"}
	m := &merger{
		parent:  parent,
		child:   &doc{id: "child"},
		pres:    &Resolved{self: "parent", origins: origins},
		origins: map[string]string{},
	}
	// The product of the two takes seconds, a pass over the sorted pointers
	// a few milliseconds. The profile package reads no clock, so the bound is
	// a context's.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range layers {
			m.leaf(parent, nil, fmt.Sprintf("/layers/l%d", i))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("%d values over %d origins took longer than 700 ms", layers, 2*layers)
	}
	if len(m.origins) != 2*layers {
		t.Errorf("%d origins, want %d", len(m.origins), 2*layers)
	}
	for _, i := range []int{0, 1, 10, 11, 100, layers - 1} {
		south, east := fmt.Sprintf("/layers/l%d/south", i), fmt.Sprintf("/layers/l%d/east", i)
		if m.origins[south] != "grand" || m.origins[east] != "parent" {
			t.Errorf("layer %d: %q %q", i, m.origins[south], m.origins[east])
		}
	}
	if _, ok := m.origins["/layers/l1"]; ok {
		t.Error("a value that has origins below it is named as a whole")
	}
	// A value without any is named after the profile that wrote it.
	m.leaf(parent, nil, "/layers/other")
	if got := m.origins["/layers/other"]; got != "parent" {
		t.Errorf("origin of a plain value: %q", got)
	}
}
