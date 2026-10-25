package input

import (
	"flag"
	"fmt"
	"slices"
	"strings"
)

// SDLFlags are the --preset and --hint flags that padmated, the
// padmatectl commands reading SDL and spk01 share.
type SDLFlags struct {
	Preset string   // the name of a preset of Presets
	Extra  HintList // set after the preset's hints
}

// AddFlags defines the flags on fs: --preset, "default" unless set, and
// the repeatable --hint NAME=VALUE.
func (f *SDLFlags) AddFlags(fs *flag.FlagSet) {
	fs.StringVar(&f.Preset, "preset", "default", "the SDL hint preset")
	fs.Var(&f.Extra, "hint", "an SDL hint NAME=VALUE; repeatable")
}

// Hints returns the hints of the preset and then those of --hint, for
// Options.Hints. It fails when there is no preset of that name.
func (f *SDLFlags) Hints() ([]Hint, error) {
	preset, ok := PresetHints(f.Preset)
	if !ok {
		return nil, fmt.Errorf("unknown preset %q; want one of %s", f.Preset, strings.Join(PresetNames(), ", "))
	}
	return slices.Concat(preset, f.Extra), nil
}
