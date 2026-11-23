package events

import (
	"errors"
	"fmt"
	"sync"
)

var (
	builtinMu         sync.Mutex
	builtinRegistrars []func(*Registry)
)

// registerBuiltin appends a domain schema registrar executed by RegisterBuiltins.
func registerBuiltin(fn func(*Registry)) {
	builtinMu.Lock()
	builtinRegistrars = append(builtinRegistrars, fn)
	builtinMu.Unlock()
}

// RegisterBuiltins registers every compiled-in Ether event schema on r.
// Calling RegisterBuiltins more than once on the same registry is safe.
func RegisterBuiltins(r *Registry) {
	if r == nil {
		return
	}
	builtinMu.Lock()
	fns := append(([]func(*Registry))(nil), builtinRegistrars...)
	builtinMu.Unlock()
	for _, fn := range fns {
		fn(r)
	}
}

// NewBuiltinRegistry returns a Registry pre-populated with all built-in Ether
// event schemas.
func NewBuiltinRegistry() *Registry {
	r := NewRegistry()
	RegisterBuiltins(r)
	return r
}

// mustRegisterBuiltin registers a typed built-in schema, ignoring duplicate
// registrations so domain registrars are idempotent. Any schema definition
// bug panics immediately during initialization.
func mustRegisterBuiltin[T any](r *Registry, meta EventSchema) {
	if r == nil {
		return
	}
	if err := RegisterStruct[T](r, meta); err != nil && !errors.Is(err, ErrDuplicateSchema) {
		panic(fmt.Sprintf("events: register builtin %q: %v", meta.Type, err))
	}
}
