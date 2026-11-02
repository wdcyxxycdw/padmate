package main

import (
	"errors"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// parseFunctionKey reads the value of --function-key (ADR-0012, ADR-0017):
// one of the controls that can be a function key, by its canonical name, as
// config.json's devices.<DeviceKey>.functionKey writes it: start, back or a
// a shoulder button or a trigger (lt, rt). validate reads no config.json, so the flag stands in for it, and
// trace has it override the controller's own.
func parseFunctionKey(s string) (control.Control, error) {
	if c, ok := control.Parse(s); ok && c.String() == s && c.CanBeFunctionKey() {
		return c, nil
	}
	return control.Invalid, errors.New("--function-key must be start, back, leftshoulder, rightshoulder, lt or rt")
}
