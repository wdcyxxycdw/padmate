package main

import (
	"reflect"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
)

// paramsOnlyChange compares resolved profiles, including every script option
// except its parameters. Ordinary configuration changes retain safe reload.
func paramsOnlyChange(before, after *profile.Profile) bool {
	if before == nil || after == nil || before.Script == nil || after.Script == nil {
		return false
	}
	oldProfile, newProfile := *before, *after
	oldScript, newScript := *before.Script, *after.Script
	oldScript.Params, newScript.Params = nil, nil
	oldProfile.Script, newProfile.Script = &oldScript, &newScript
	return reflect.DeepEqual(oldProfile, newProfile)
}

// PrepareParams is called exclusively by the slot transaction. Paused slots
// validate with their last detached schema; a never-started slot has no schema
// yet and validates when its first worker loads the committed configuration.
func (r *scriptRuntime) PrepareParams(raw map[string]any) (func(), error) {
	if r.lifecycle == nil && r.paramsSchema != nil {
		if err := script.ValidateParams(r.paramsSchema, raw); err != nil {
			return nil, err
		}
	}
	var commits []func()
	for _, life := range []*script.Lifecycle{r.lifecycle, pendingLifecycle(r.pending)} {
		if life == nil {
			continue
		}
		commit, err := life.PrepareParams(raw)
		if err != nil {
			return nil, err
		}
		commits = append(commits, commit)
	}
	return func() {
		for _, commit := range commits {
			commit()
		}
	}, nil
}

func pendingLifecycle(p *pendingScriptReload) *script.Lifecycle {
	if p == nil {
		return nil
	}
	return p.life
}
