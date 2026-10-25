package script

import (
	"sync"

	"github.com/dop251/goja"
)

// Session belongs to a supervisor profile key, rather than a runtime or slot.
// Serialized values never retain JavaScript objects across runtime boundaries.
type Session struct {
	mu     sync.RWMutex
	values map[string]string
}

// NewSession creates an empty in-memory profile session.
func NewSession() *Session { return &Session{values: make(map[string]string)} }

// SessionStore holds sessions until the supervisor is discarded.
type SessionStore struct {
	mu       sync.Mutex
	profiles map[string]*Session
}

// ForProfile obtains one session for an explicit stable profile key.
func (s *SessionStore) ForProfile(key string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.profiles == nil {
		s.profiles = make(map[string]*Session)
	}
	if s.profiles[key] == nil {
		s.profiles[key] = NewSession()
	}
	return s.profiles[key]
}

func (s *Session) snapshot() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	values := make(map[string]string, len(s.values))
	for k, v := range s.values {
		values[k] = v
	}
	return values
}

type sessionView struct {
	shared  *Session
	values  map[string]string
	changes map[string]*string
	live    bool
}

func (s *Session) fork() *sessionView {
	return &sessionView{shared: s, values: s.snapshot(), changes: make(map[string]*string)}
}
func (v *sessionView) get(k string) (string, bool) {
	if v.live {
		v.shared.mu.RLock()
		defer v.shared.mu.RUnlock()
		value, ok := v.shared.values[k]
		return value, ok
	}
	value, ok := v.values[k]
	return value, ok
}
func (v *sessionView) set(k string, value *string) {
	if v.live {
		v.shared.mu.Lock()
		defer v.shared.mu.Unlock()
		if v.shared.values == nil {
			v.shared.values = make(map[string]string)
		}
		if value == nil {
			delete(v.shared.values, k)
		} else {
			v.shared.values[k] = *value
		}
		return
	}
	if value == nil {
		delete(v.values, k)
	} else {
		v.values[k] = *value
	}
	v.changes[k] = value
}
func (v *sessionView) activate() {
	if v.live {
		return
	}
	v.shared.mu.Lock()
	defer v.shared.mu.Unlock()
	if v.shared.values == nil {
		v.shared.values = make(map[string]string)
	}
	for k, value := range v.changes {
		if value == nil {
			delete(v.shared.values, k)
		} else {
			v.shared.values[k] = *value
		}
	}
	v.values = nil
	v.changes = nil
	v.live = true
}
func (v *sessionView) refresh() {
	if v.live {
		return
	}
	v.values = v.shared.snapshot()
	for k, value := range v.changes {
		if value == nil {
			delete(v.values, k)
		} else {
			v.values[k] = *value
		}
	}
}
func (v *sessionView) detach() {
	if v.live {
		v.values = v.shared.snapshot()
		v.changes = make(map[string]*string)
		v.live = false
	}
}

func (h *Host) installSessionAPI(o *goja.Object) {
	json := h.VM.Get("JSON").ToObject(h.VM)
	parse, _ := goja.AssertFunction(json.Get("parse"))
	stringify, _ := goja.AssertFunction(json.Get("stringify"))
	kv := h.VM.NewObject()
	setFunction(h.VM, kv, "get", 1, func(call goja.FunctionCall) goja.Value {
		value, ok := h.session.get(call.Argument(0).String())
		if !ok {
			return goja.Undefined()
		}
		result, err := parse(goja.Undefined(), h.VM.ToValue(value))
		if err != nil {
			panic(err)
		}
		return result
	})
	setFunction(h.VM, kv, "set", 2, func(call goja.FunctionCall) goja.Value {
		value, err := stringify(goja.Undefined(), call.Argument(1))
		if err != nil {
			panic(err)
		}
		if goja.IsUndefined(value) {
			panic(h.VM.NewTypeError("session.set: value must be JSON serializable"))
		}
		encoded := value.String()
		h.session.set(call.Argument(0).String(), &encoded)
		return goja.Undefined()
	})
	setFunction(h.VM, kv, "delete", 1, func(call goja.FunctionCall) goja.Value {
		h.session.set(call.Argument(0).String(), nil)
		return goja.Undefined()
	})
	set(o, "session", kv)
}
