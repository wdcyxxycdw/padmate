package script

import "fmt"

// PrepareParams validates a replacement and creates its immutable VM value.
// Both preparation and the returned commit must run on the host's owner loop.
// Discarding the commit leaves the existing context untouched.
func (h *Host) PrepareParams(raw map[string]any) (func(), error) {
	if h.disposed {
		return nil, fmt.Errorf("script is disposed")
	}
	values, err := normalizeParams(h.paramSchema, raw)
	if err != nil {
		return nil, err
	}
	next := h.readonlyValues(values)
	return func() { h.paramsValue = next }, nil
}

// ParamsSchema returns detached metadata for validation while a runtime is
// dormant. Call it on the VM's owner loop before disposing the host.
func (h *Host) ParamsSchema() map[string]ParamInfo {
	return cloneParamsSchema(h.paramSchema)
}

// ParamsSchema keeps the last successful schema available while a lifecycle
// has no current host, such as automatic restart backoff or after pause.
func (l *Lifecycle) ParamsSchema() map[string]ParamInfo {
	return cloneParamsSchema(l.paramsSchema)
}

func cloneParamsSchema(original map[string]ParamInfo) map[string]ParamInfo {
	if original == nil {
		return nil
	}
	schema := make(map[string]ParamInfo, len(original))
	for name, spec := range original {
		schema[name] = spec.clone()
	}
	return schema
}

// ValidateParams uses detached metadata and never touches a JavaScript VM.
func ValidateParams(schema map[string]ParamInfo, raw map[string]any) error {
	_, err := normalizeParams(schema, raw)
	return err
}

// PrepareParams updates the current context and the options inherited by
// future workers without restarting the lifecycle. A worker already running
// keeps its snapshot; Swap refreshes it after ownership transfers back.
func (l *Lifecycle) PrepareParams(raw map[string]any) (func(), error) {
	cloned, err := cloneJSONValues(raw)
	if err != nil {
		return nil, err
	}
	var commitHost func()
	if l.current != nil && !l.current.disposed {
		commitHost, err = l.current.PrepareParams(cloned)
		if err != nil {
			return nil, err
		}
	} else if l.paramsSchema != nil {
		if err := ValidateParams(l.paramsSchema, cloned); err != nil {
			return nil, err
		}
	}
	return func() {
		if commitHost != nil {
			commitHost()
		}
		l.hostOptions.Params = cloned
	}, nil
}
