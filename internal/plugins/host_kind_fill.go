package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
)

// A frontend adapter's viewer opens a container or searches the same way a
// viewer in an Ether app does: Ether expands the container or runs the
// kind's live search and stores what it finds. A pull-synced kind takes no
// pushes, so an adapter never writes catalogue items itself; it asks Ether
// (kinds.expand / kinds.search) and then reads the result with kinds.browse.

// DefaultKindSearchLimit and MaxKindSearchLimit bound one kinds.search.
const (
	DefaultKindSearchLimit = 50
	MaxKindSearchLimit     = 200
)

// KindExpandResult answers kinds.expand. Pending means children are still
// arriving after the wait an app's open gets; browse again shortly.
type KindExpandResult struct {
	Pending bool `json:"pending"`
}

// KindSearchResult answers kinds.search with how many matches Ether holds
// for the query; read them with kinds.browse({query}).
type KindSearchResult struct {
	Found int `json:"found"`
}

func (h *host) adapterViewer(ctx context.Context, kindKey, op string) (int64, error) {
	if _, _, err := h.kindCheck(ctx, kindKey, CapFrontendAdapter); err != nil {
		return 0, err
	}
	viewerID, ok := ViewerFromContext(ctx)
	if !ok || viewerID <= 0 {
		return 0, fmt.Errorf("%w: %s requires an authenticated frontend adapter viewer", ErrPermissionDenied, op)
	}
	return viewerID, nil
}

// KindExpand fills a container's children for the adapter viewer, on the
// interactive path an app's open uses. A container the viewer may not see
// is not found.
func (h *host) KindExpand(ctx context.Context, kindKey, parentID string) (KindExpandResult, error) {
	if !ValidExternalID(parentID) {
		return KindExpandResult{}, &ValidationError{Fields: []FieldError{{Field: "parentId", Message: "parentId must be a valid external id"}}}
	}
	viewerID, err := h.adapterViewer(ctx, kindKey, "kinds.expand")
	if err != nil {
		return KindExpandResult{}, err
	}
	backend := h.m.kindBackends().KindExpand
	if backend == nil {
		return KindExpandResult{}, ErrUnavailable
	}
	pending, err := backend(ctx, h.plugin, viewerID, kindKey, parentID)
	if errors.Is(err, core.ErrNotFound) {
		return KindExpandResult{}, fmt.Errorf("%w: %s", ErrNotFound, parentID)
	}
	return KindExpandResult{Pending: pending}, err
}

// KindSearch runs the kind's search for the adapter viewer the way an app's
// search does, live plugin search included, so its finds are in Ether.
func (h *host) KindSearch(ctx context.Context, kindKey, query string, limit int) (KindSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" || utf8.RuneCountInString(query) > 500 {
		return KindSearchResult{}, &ValidationError{Fields: []FieldError{{Field: "query", Message: "query must be 1 to 500 characters"}}}
	}
	if limit <= 0 {
		limit = DefaultKindSearchLimit
	}
	limit = min(limit, MaxKindSearchLimit)
	viewerID, err := h.adapterViewer(ctx, kindKey, "kinds.search")
	if err != nil {
		return KindSearchResult{}, err
	}
	backend := h.m.kindBackends().KindLiveSearch
	if backend == nil {
		return KindSearchResult{}, ErrUnavailable
	}
	found, err := backend(ctx, h.plugin, viewerID, kindKey, query, limit)
	return KindSearchResult{Found: found}, err
}

// callKindFill dispatches the hosted kinds.expand / kinds.search calls.
func (h *host) callKindFill(ctx context.Context, name string, args []json.RawMessage) (any, error, bool) {
	str := func(i int) string {
		var s string
		if i < len(args) {
			_ = json.Unmarshal(args[i], &s)
		}
		return s
	}
	switch name {
	case "kinds.expand":
		out, err := h.KindExpand(ctx, str(0), str(1))
		return out, err, true
	case "kinds.search":
		var opts struct {
			Limit int `json:"limit"`
		}
		if len(args) > 2 && string(args[2]) != "null" {
			if err := json.Unmarshal(args[2], &opts); err != nil {
				return nil, &wasm.Fault{Code: "bad_arguments", Message: "kinds.search takes (kind, query, { limit? })"}, true
			}
		}
		out, err := h.KindSearch(ctx, str(0), str(1), opts.Limit)
		return out, err, true
	}
	return nil, nil, false
}
