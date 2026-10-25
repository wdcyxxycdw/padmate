package main

import (
	"errors"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// checkEsbuild makes sure esbuild accepts every name in supported, so a
// stale table fails generation instead of the first script build.
func checkEsbuild(supported map[string]bool) error {
	r := api.Transform("", api.TransformOptions{Supported: supported, LogLevel: api.LogLevelSilent})
	if len(r.Errors) == 0 {
		return nil
	}
	msgs := make([]string, len(r.Errors))
	for i, m := range r.Errors {
		msgs[i] = "esbuild: " + m.Text
	}
	return errors.New(strings.Join(msgs, "\n"))
}
