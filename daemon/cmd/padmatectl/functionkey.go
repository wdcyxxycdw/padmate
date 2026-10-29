package main

import (
	"errors"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// parseFunctionKey reads the value of --function-key (MAP-27, ADR-0075): one of
// the controls that can be the function key, by its canonical name, as
// config.json's functionKey writes it. validate and trace read no config.json,
// so the flag stands in for it.
func parseFunctionKey(s string) (control.Control, error) {
	if c, ok := control.Parse(s); ok && c.String() == s && c.CanBeFunctionKey() {
		return c, nil
	}
	return control.Invalid, errors.New("--function-key must be start, back or guide")
}
