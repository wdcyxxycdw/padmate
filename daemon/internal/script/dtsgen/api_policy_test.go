package dtsgen

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// The script API policy (M14, docs/SCRIPTING.md §6): the host runs the
// current script API and the previous 0.N, and script.api in profile.json
// names one of them.

func TestSupportedAPIsFollowThePolicy(t *testing.T) {
	if len(SupportedAPIs) == 0 || len(SupportedAPIs) > 2 {
		t.Fatalf("SupportedAPIs = %v; the policy is the current API and the previous 0.N", SupportedAPIs)
	}
	if HostAPI != SupportedAPIs[0] {
		t.Fatalf("HostAPI = %s, want the current API %s (first of SupportedAPIs)", HostAPI, SupportedAPIs[0])
	}
	// The previous 0.N is the last API version a release shipped, which need
	// not be N-1: the API follows the milestone that changes it.
	for i := 1; i < len(SupportedAPIs); i++ {
		prev, cur := SupportedAPIs[i-1], SupportedAPIs[i]
		if cur.Major != prev.Major || cur.Minor >= prev.Minor {
			t.Fatalf("SupportedAPIs = %v; each is an earlier 0.N than the one before it", SupportedAPIs)
		}
	}
	if HostAPI.Major != 0 {
		t.Fatalf("API %s: the 1.0 rules (SCR-35) are not written yet", HostAPI)
	}
}

func TestValidateHostAPIAcceptsTheSupportedVersions(t *testing.T) {
	for _, api := range SupportedAPIs {
		if err := ValidateHostAPI(api); err != nil {
			t.Errorf("API %s: %v", api, err)
		}
		if !Supported(api) {
			t.Errorf("Supported(%s) = false", api)
		}
	}
	next := Version{Major: HostAPI.Major, Minor: HostAPI.Minor + 1}
	older := Version{Major: HostAPI.Major, Minor: SupportedAPIs[len(SupportedAPIs)-1].Minor - 1}
	for _, api := range []Version{next, older, {Major: 1}, {}} {
		err := ValidateHostAPI(api)
		if err == nil || Supported(api) {
			t.Errorf("API %s accepted", api)
			continue
		}
		for _, want := range []string{api.String(), "host API " + HostAPI.String()} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
		}
	}
}

// script.api in the profile schema lists exactly SupportedAPIs, current
// first: change one without the other and this fails.
func TestSchemaScriptAPIMatchesSupportedAPIs(t *testing.T) {
	var schema struct {
		Defs struct {
			Script struct {
				Properties struct {
					API struct {
						Enum  []string `json:"enum"`
						Const *string  `json:"const"`
					} `json:"api"`
				} `json:"properties"`
			} `json:"script"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(profile.Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	api := schema.Defs.Script.Properties.API
	if api.Const != nil {
		t.Fatalf("script.api is a const (%q); it is an enum of SupportedAPIs", *api.Const)
	}
	if want := SupportedNames(); !slices.Equal(api.Enum, want) {
		t.Fatalf("schema script.api enum = %q, SupportedAPIs = %q", api.Enum, want)
	}
	if profile.CurrentScriptAPI != HostAPI.String() {
		t.Fatalf("profile.CurrentScriptAPI = %s, HostAPI = %s", profile.CurrentScriptAPI, HostAPI)
	}
}

// Every value of the padmate module in the current API has a stability
// (StableMembers or PreviewMembers), so a member added later cannot be
// forgotten, and the lists name nothing the API does not have.
func TestEveryAPIMemberHasAStability(t *testing.T) {
	members, err := ValueMembers(HostAPI)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) < 15 {
		t.Fatalf("only %d members found: %v", len(members), members)
	}
	seen := map[string]string{}
	for _, list := range []struct {
		kind  string
		names []string
	}{{"stable", StableMembers}, {"preview", PreviewMembers}} {
		for _, name := range list.names {
			if before, dup := seen[name]; dup {
				t.Errorf("%s is listed as %s and as %s", name, before, list.kind)
			}
			seen[name] = list.kind
			if !slices.Contains(members, name) {
				t.Errorf("%s member %q is not in API %s", list.kind, name, HostAPI)
			}
		}
	}
	for _, name := range members {
		if seen[name] == "" {
			t.Errorf("member %q of API %s is neither stable nor preview; decide in api_policy.go", name, HostAPI)
		}
	}
}

// The members a macro can use (SCR-50) are the oldest and most used part of
// the API: they are stable, whatever the scripts' stable subset says.
func TestMacroMembersAreStable(t *testing.T) {
	macro, err := MacroMembers(HostAPI)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range macro {
		if !slices.Contains(StableMembers, name) {
			t.Errorf("macro member %q is not stable", name)
		}
	}
	// The core the documentation names (SCRIPTING §6).
	for _, name := range []string{"on", "key", "layers", "pad"} {
		if !slices.Contains(StableMembers, name) {
			t.Errorf("core member %q is not stable", name)
		}
	}
}

func TestValueMembers(t *testing.T) {
	const src = `declare module "padmate" {
  /** @since 0.5 */
  export type Button = "a";
  /** @since 0.5 @macro */
  export const host: {
    readonly version: string;
  };
  /** @since 0.5 @until 0.6 */ export function defineScript(def: number): number;
  /** @since 0.6 */ export function defineScript(def: string): string;
  /** base 层脚本级 @since 0.5 */ export const on: Scope["on"];
  /** @since 0.7 */ export const newer: number;
  export interface Scope { export?: number }
}
declare module "padmate/math" {
  export function clamp(v: number): number;
}
`
	got, err := valueMembers(src, Version{Major: 0, Minor: 6})
	if err != nil || !slices.Equal(got, []string{"host", "defineScript", "on"}) {
		t.Fatalf("got %v, %v", got, err)
	}
}
