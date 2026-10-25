package dtsgen

import (
	"strings"
	"testing"
)

func TestFilterReplacedSignature(t *testing.T) {
	const source = `declare module "test" {
  /** @since 0.5 @until 0.6 */ export function setup(): void;
  /** @since 0.6 */ export function setup(ctx: object): void;
}`
	for _, tc := range []struct {
		version      Version
		want, absent string
	}{
		{Version{0, 5}, "setup()", "setup(ctx"},
		{Version{0, 6}, "setup(ctx", "setup()"},
	} {
		got, err := FilterString(source, tc.version)
		if err != nil || !strings.Contains(got, tc.want) || strings.Contains(got, tc.absent) {
			t.Errorf("Filter(%s) = %s, %v", tc.version, got, err)
		}
	}
}
