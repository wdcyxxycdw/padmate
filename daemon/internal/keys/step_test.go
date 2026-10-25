package keys_test

import (
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

func TestStepString(t *testing.T) {
	tests := []struct {
		name string
		step keys.Step
		want string
	}{
		{"keys", keys.KeysStep(keys.MustParse("ctrl+a")), "Ctrl+A"},
		{"wait", keys.WaitStep(100 * time.Millisecond), "wait 100"},
		{"text", keys.TextStep("你好"), `text "你好"`},
		{"newline is escaped", keys.TextStep("a\nb"), `text "a\nb"`},
		{"quote is escaped", keys.TextStep(`say "hi"`), `text "say \"hi\""`},
		{"long text is cut", keys.TextStep(strings.Repeat("字", 40)), `text "` + strings.Repeat("字", keys.ShortTextRunes) + `…"`},
		{"text of the limit is whole", keys.TextStep(strings.Repeat("a", keys.ShortTextRunes)), `text "` + strings.Repeat("a", keys.ShortTextRunes) + `"`},
		{"invalid", keys.Step{}, "step(0)"},
	}
	for _, tt := range tests {
		if got := tt.step.String(); got != tt.want {
			t.Errorf("%s: String() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestFormatSteps(t *testing.T) {
	steps := []keys.Step{
		keys.KeysStep(keys.MustParse("Ctrl+A")),
		keys.WaitStep(100 * time.Millisecond),
		keys.TextStep("hi"),
	}
	if got, want := keys.FormatSteps(steps), `Ctrl+A, wait 100, text "hi"`; got != want {
		t.Errorf("FormatSteps = %q, want %q", got, want)
	}
	if got := keys.FormatSteps(nil); got != "" {
		t.Errorf("FormatSteps(nil) = %q, want empty", got)
	}
}

func TestShortText(t *testing.T) {
	if got := keys.ShortText("abc", 5); got != "abc" {
		t.Errorf("ShortText short = %q", got)
	}
	if got := keys.ShortText("abcdef", 5); got != "abcde…" {
		t.Errorf("ShortText long = %q", got)
	}
	// A cut never splits a character, a pair of units included.
	if got := keys.ShortText("a😀😀😀", 2); got != "a😀…" {
		t.Errorf("ShortText pairs = %q", got)
	}
	if got := keys.ShortText("\xff\xffabc", 2); got != "\xff\xff…" {
		t.Errorf("ShortText of invalid UTF-8 = %q", got)
	}
}

func TestValidateStep(t *testing.T) {
	tests := []struct {
		name string
		step keys.Step
		want string // substring of the error, "" for a valid step
	}{
		{"keys", keys.KeysStep(keys.MustParse("F13")), ""},
		{"wait minimum", keys.WaitStep(time.Millisecond), ""},
		{"wait maximum", keys.WaitStep(10 * time.Second), ""},
		{"text", keys.TextStep("a"), ""},
		{"no kind", keys.Step{}, "kind"},
		{"keys without a chord", keys.Step{Kind: keys.StepKeys}, "chord"},
		{"wait zero", keys.WaitStep(0), "wait"},
		{"wait negative", keys.WaitStep(-time.Millisecond), "wait"},
		{"wait too long", keys.WaitStep(10*time.Second + time.Millisecond), "wait"},
		{"empty text", keys.TextStep(""), "empty"},
		{"control character", keys.TextStep("a\rb"), "U+000D"},
		{"too long text", keys.TextStep(strings.Repeat("a", 257)), "too long"},
		{"keys step with a text", keys.Step{Kind: keys.StepKeys, Chord: keys.MustParse("A"), Text: "x"}, "text"},
		{"wait step with a chord", keys.Step{Kind: keys.StepWait, Wait: time.Second, Chord: keys.MustParse("A")}, "chord"},
		{"text step with a wait", keys.Step{Kind: keys.StepText, Text: "x", Wait: time.Second}, "wait"},
	}
	for _, tt := range tests {
		err := keys.ValidateStep(tt.step)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%s: ValidateStep = %v, want nil", tt.name, err)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
			t.Errorf("%s: ValidateStep = %v, want an error with %q", tt.name, err, tt.want)
		}
	}
}

func TestValidateSteps(t *testing.T) {
	step := keys.KeysStep(keys.MustParse("A"))
	if err := keys.ValidateSteps(nil); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Errorf("ValidateSteps(nil) = %v", err)
	}
	if err := keys.ValidateSteps(make([]keys.Step, 1)); err == nil || !strings.Contains(err.Error(), "step 0") {
		t.Errorf("ValidateSteps(invalid step) = %v, want the step named", err)
	}
	full := make([]keys.Step, keys.MaxSteps)
	for i := range full {
		full[i] = step
	}
	if err := keys.ValidateSteps(full); err != nil {
		t.Errorf("ValidateSteps(%d steps) = %v", len(full), err)
	}
	if err := keys.ValidateSteps(append(full, step)); err == nil || !strings.Contains(err.Error(), "at most 64") {
		t.Errorf("ValidateSteps(%d steps) = %v", len(full)+1, err)
	}
	bad := append([]keys.Step{step, keys.WaitStep(time.Second)}, keys.TextStep(""))
	if err := keys.ValidateSteps(bad); err == nil || !strings.Contains(err.Error(), "step 2") {
		t.Errorf("ValidateSteps = %v, want step 2 named", err)
	}
}
