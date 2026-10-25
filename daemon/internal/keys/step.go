package keys

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The limits of a sequence (ADR-0070, MAP-22): at least one and at most MaxSteps
// steps, and a wait of MinWait to MaxWait.
const (
	MaxSteps = 64
	MinWait  = time.Millisecond
	MaxWait  = 10 * time.Second
)

// ShortTextRunes is how many characters of a text Step.String shows before it
// cuts the text with an ellipsis: decisions and the dry-run show the action
// in one short line.
const ShortTextRunes = 16

// StepKind is what a Step does. The zero value is invalid.
type StepKind uint8

const (
	// StepKeys taps a shortcut, as one item of a key sequence does.
	StepKeys StepKind = iota + 1
	// StepWait waits, counted from the previous step's physical release.
	StepWait
	// StepText types a text (ValidateText), a character at a time.
	StepText
)

// Step is one step of a sequence action (ADR-0070, MAP-22), of the
// key.sequence of a script (SCR-26), and the one step of a text action. It is
// plain data, shared by the profile, the engine and the output layer: only the
// member of its Kind is used. Build one with KeysStep, WaitStep or TextStep.
type Step struct {
	Kind  StepKind
	Chord Chord         // StepKeys
	Wait  time.Duration // StepWait
	Text  string        // StepText
}

// KeysStep returns the step that taps c.
func KeysStep(c Chord) Step { return Step{Kind: StepKeys, Chord: c} }

// WaitStep returns the step that waits d.
func WaitStep(d time.Duration) Step { return Step{Kind: StepWait, Wait: d} }

// TextStep returns the step that types text.
func TextStep(text string) Step { return Step{Kind: StepText, Text: text} }

// String shows the step as a decision shows it: the shortcut, "wait 100" (in
// milliseconds) or `text "…"`, a text cut after ShortTextRunes characters and
// quoted as Go quotes it, so that a newline stays on the line.
func (s Step) String() string {
	switch s.Kind {
	case StepKeys:
		return s.Chord.String()
	case StepWait:
		return "wait " + strconv.FormatInt(s.Wait.Milliseconds(), 10)
	case StepText:
		return "text " + quoteShort(s.Text)
	}
	return "step(" + strconv.Itoa(int(s.Kind)) + ")"
}

// quoteShort quotes the first ShortTextRunes characters of s; an ellipsis,
// inside the quotes, tells that the text goes on.
func quoteShort(s string) string {
	short := ShortText(s, ShortTextRunes)
	if short == s {
		return strconv.Quote(s)
	}
	q := strconv.Quote(strings.TrimSuffix(short, "…"))
	return q[:len(q)-1] + "…\""
}

// ShortText returns s, or its first limit characters followed by an ellipsis
// when it has more. It cuts between characters (a character outside the Basic
// Multilingual Plane is one), and a byte that is not valid UTF-8 counts as one.
func ShortText(s string, limit int) string {
	n := 0
	for i := range s {
		if n == limit {
			return s[:i] + "…"
		}
		n++
	}
	return s
}

// FormatSteps shows the steps as a decision shows a sequence: the steps in
// Step.String's form, apart by a comma, such as `Ctrl+A, wait 100, text "hi"`.
func FormatSteps(steps []Step) string {
	parts := make([]string, len(steps))
	for i, s := range steps {
		parts[i] = s.String()
	}
	return strings.Join(parts, ", ")
}

// ValidateStep checks a step by the rules a profile applies (ADR-0070): a keys
// step has a valid chord, a wait lies in MinWait to MaxWait, a text passes
// ValidateText, and a step has nothing of the other kinds. It returns an error
// whose text names what is wrong, not where; a *TextError for a bad text.
func ValidateStep(s Step) error {
	switch s.Kind {
	case StepKeys:
		switch {
		case !s.Chord.IsValid():
			return errors.New("a keys step needs a chord")
		case s.Wait != 0 || s.Text != "":
			return errors.New("a keys step has no wait or text")
		}
	case StepWait:
		switch {
		case s.Wait < MinWait || s.Wait > MaxWait:
			return fmt.Errorf("a wait is %d to %d ms, not %d", MinWait.Milliseconds(), MaxWait.Milliseconds(), s.Wait.Milliseconds())
		case s.Chord.IsValid() || s.Text != "":
			return errors.New("a wait step has no chord or text")
		}
	case StepText:
		if s.Wait != 0 || s.Chord.IsValid() {
			return errors.New("a text step has no chord or wait")
		}
		return ValidateText(s.Text)
	default:
		return fmt.Errorf("unknown step kind %d", s.Kind)
	}
	return nil
}

// ValidateSteps checks a sequence: 1 to MaxSteps steps, each passing
// ValidateStep. The error of a step names its index.
func ValidateSteps(steps []Step) error {
	switch {
	case len(steps) == 0:
		return errors.New("a sequence needs at least one step")
	case len(steps) > MaxSteps:
		return fmt.Errorf("a sequence has at most %d steps, not %d", MaxSteps, len(steps))
	}
	for i, s := range steps {
		if err := ValidateStep(s); err != nil {
			return fmt.Errorf("step %d: %w", i, err)
		}
	}
	return nil
}
