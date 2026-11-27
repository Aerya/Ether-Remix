package personalize

import (
	"fmt"
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
)

// RowSourceKind is one row source the Recommendations designer offers:
// a built-in row family (kinds.go, sources.go), or a runtime source such
// as a plugin list. Its Info is what "What fills this row?" shows; a page
// row names it by Info().Kind.
//
// To add a built-in row family: add its kindInfo in kinds.go (title,
// description, group, needs, params) and its builder; it appears here
// automatically. To add a runtime source: implement RowSourceKind and call
// Engine.RegisterRowSourceKind.
type RowSourceKind interface {
	Info() core.RowSourceInfo
}

type staticSource struct{ info core.RowSourceInfo }

func (s staticSource) Info() core.RowSourceInfo { return s.info }

// RowKindRegistry holds the row sources. Safe for concurrent use.
type RowKindRegistry struct {
	mu    sync.RWMutex
	kinds map[string]RowSourceKind
	order []string
}

func NewRowKindRegistry() *RowKindRegistry {
	r := &RowKindRegistry{kinds: map[string]RowSourceKind{}}
	for _, info := range builtinSources() {
		if err := r.Register(staticSource{info}); err != nil {
			panic("personalize: " + err.Error())
		}
	}
	return r
}

func (r *RowKindRegistry) Register(k RowSourceKind) error {
	if k == nil || k.Info().Kind == "" {
		return fmt.Errorf("row source needs a kind")
	}
	kind := k.Info().Kind
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.kinds[kind]; ok {
		return fmt.Errorf("row source %q already registered", kind)
	}
	r.kinds[kind] = k
	r.order = append(r.order, kind)
	return nil
}

// Unregister removes a runtime row source by kind.
func (r *RowKindRegistry) Unregister(kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.kinds[kind]; !ok {
		return
	}
	delete(r.kinds, kind)
	out := r.order[:0]
	for _, cur := range r.order {
		if cur != kind {
			out = append(out, cur)
		}
	}
	r.order = out
}

// Source returns a source's description.
func (r *RowKindRegistry) Source(kind string) (core.RowSourceInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k, ok := r.kinds[kind]
	if !ok {
		return core.RowSourceInfo{}, false
	}
	return k.Info(), true
}

// Sources lists every source in registration order.
func (r *RowKindRegistry) Sources() []core.RowSourceInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]core.RowSourceInfo, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.kinds[id].Info())
	}
	return out
}

// Descriptors returns the extend rowSource descriptors for every source.
func (r *RowKindRegistry) Descriptors() []extend.Descriptor {
	srcs := r.Sources()
	out := make([]extend.Descriptor, 0, len(srcs))
	for i, s := range srcs {
		out = append(out, extend.Descriptor{
			Kind: extend.KindRowSource, ID: s.Kind, Owner: extend.Builtin,
			Label: extend.Text{Source: s.Title}, Hint: extend.Text{Source: s.Description},
			Group: s.Group, Order: (i + 1) * 10,
		}.WithMeta(extend.RowSourceMeta{HubID: s.Kind, RowKind: s.Kind, Source: infoFor(s.Kind).source}))
	}
	return out
}

var defaultRowKinds = NewRowKindRegistry()

// BuiltinRowDescriptors returns extend descriptors for the built-in row
// sources, without an Engine (app startup registers these).
func BuiltinRowDescriptors() []extend.Descriptor { return defaultRowKinds.Descriptors() }

func (e *Engine) rowKindRegistry() *RowKindRegistry {
	if e.rowKinds != nil {
		return e.rowKinds
	}
	return defaultRowKinds
}

// RegisterRowSourceKind adds a row source to this engine.
func (e *Engine) RegisterRowSourceKind(k RowSourceKind) error { return e.rowKindRegistry().Register(k) }

// UnregisterRowSourceKind removes a runtime row source from this engine.
func (e *Engine) UnregisterRowSourceKind(kind string) { e.rowKindRegistry().Unregister(kind) }

// RowKindRegistry exposes this engine's row-source registry.
func (e *Engine) RowKindRegistry() *RowKindRegistry { return e.rowKindRegistry() }
