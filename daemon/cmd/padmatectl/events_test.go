package main

import (
	"bytes"
	"context"
	"testing"
)

func TestEventCLIRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"--timeout=0"}, {"--duration=-1s"}, {"--count=-1"}, {"unknown"}, {"--timeout=2m"}} {
		var out, err bytes.Buffer
		if code := eventsMain(context.Background(), "subscribe", args, &out, &err); code != 2 {
			t.Fatal(args, code, &err)
		}
	}
}
