package vela

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// searchCursor is an auto search's continuation: the offset of the next
// page, bound to the query and its filters so a cursor never pages another
// search.
type searchCursor struct {
	Mode   string `json:"m"`
	Scope  string `json:"s"`
	Offset int    `json:"o"`
}

// searchCursorScope binds a cursor to everything that shapes the results.
func searchCursorScope(b searchRequest) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{strings.ToLower(strings.Join(strings.Fields(b.Query), " ")), b.Kind, b.Library, b.Sort, b.Direction}, "\x00")))
	return base64.RawURLEncoding.EncodeToString(sum[:12])
}

func encodeSearchCursor(b searchRequest, offset int) string {
	raw, _ := json.Marshal(searchCursor{Mode: "auto", Scope: searchCursorScope(b), Offset: offset})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeSearchCursor(cursor string, b searchRequest) (int, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, false
	}
	var c searchCursor
	if json.Unmarshal(raw, &c) != nil || c.Mode != "auto" || c.Scope != searchCursorScope(b) || c.Offset <= 0 || c.Offset >= 1000 {
		return 0, false
	}
	return c.Offset, true
}

// autoSearch answers mode "auto" (features.unifiedSearch): one ranked list.
// Title, franchise, person and fuzzy matches first; for a described query
// (AI configured) the Collections Describe titles lead. It never waits on
// a model, a provider or a plugin's remote catalogue: state "pending" with
// retry asks the client to repeat the same search, whose answer replaces
// the list.
func (i *instance) autoSearch(w http.ResponseWriter, r *http.Request, v *frontend.Viewer, token string, unified frontend.UnifiedSearcher, b searchRequest, iq frontend.ItemQuery, targetView *frontend.View, kindLibrary frontend.LibraryID) {
	ctx := i.host.Experience.NoProviderWait(r.Context())
	var page frontend.UnifiedSearchPage
	switch {
	case targetView != nil || kindLibrary != 0:
		viewID := ""
		if targetView != nil {
			viewID = targetView.ID
		} else if viewID = i.viewForLibrary(r, v, kindLibrary); viewID == "" {
			problem(w, 400, "request", "invalid_kind")
			return
		}
		compatible := true
		if targetView != nil {
			if kindLibrary != 0 && ((targetView.PluginID == "" && targetView.KindKey == "") || (targetView.LibraryID != 0 && targetView.LibraryID != kindLibrary)) {
				compatible = false
			}
			if len(iq.Types) > 0 && targetView.Type != "" && !containsType(iq.Types, targetView.Type) {
				compatible = false
			}
		}
		page.Page.Items = []frontend.Title{}
		page.Continuation = frontend.ContinuationExhausted
		if compatible {
			// A search scoped to one library: its own matches, a plugin's
			// remote catalogue included when it has answered.
			pctx, pending := frontend.WithSearchPending(ctx)
			lq := iq
			lq.Filter.Query = b.Query
			lq.Limit = iq.Limit + 1
			lib, err := i.host.Experience.LibraryItems(pctx, v, requestDevice(r), viewID, lq)
			if err != nil {
				fail(w, err, r)
				return
			}
			page.Page = lib
			page.Continuation = frontend.ContinuationExhausted
			if len(lib.Items) > iq.Limit {
				page.Page.Items = lib.Items[:iq.Limit]
				page.Continuation = frontend.ContinuationMore
			}
			if targetView != nil {
				markLandscapeTitles(page.Page.Items, *targetView)
			}
			if pending.Pending() {
				page.Pending, page.RetryAfterSeconds = true, 1
			}
		}
		page.Match = frontend.SearchMatchTitle
		if len(page.Page.Items) == 0 {
			page.Match = frontend.SearchMatchNone
		}
	default:
		var err error
		page, err = unified.UnifiedSearch(ctx, v, requestDevice(r), b.Query, iq)
		if err != nil {
			fail(w, err, r)
			return
		}
	}
	ts, err := i.titles(r, v, token, page.Page.Items)
	if err != nil {
		fail(w, err, r)
		return
	}
	for idx := range ts {
		if idx < len(page.Page.Items) && page.Described[page.Page.Items[idx].Key] {
			ts[idx]["match"] = string(frontend.SearchMatchDescription)
		} else {
			ts[idx]["match"] = string(frontend.SearchMatchTitle)
		}
	}
	state := "complete"
	retry := object{"allowed": false}
	if page.Pending {
		state = "pending"
		retry = object{"allowed": true, "afterSeconds": max(page.RetryAfterSeconds, 1)}
	}
	out := object{"query": b.Query, "generation": b.Generation, "items": ts, "continuation": page.Continuation, "match": string(page.Match), "state": state, "retry": retry}
	if page.Continuation == frontend.ContinuationMore {
		out["nextCursor"] = encodeSearchCursor(b, iq.Offset+len(page.Page.Items))
	} else if page.Continuation == frontend.ContinuationUnknown && !page.Pending && len(page.Page.Items) >= iq.Limit && iq.Offset+len(page.Page.Items) < 1000 {
		out["continuation"] = frontend.ContinuationMore
		out["nextCursor"] = encodeSearchCursor(b, iq.Offset+len(page.Page.Items))
	} else if page.Continuation == frontend.ContinuationUnknown && !page.Pending {
		out["continuation"] = frontend.ContinuationExhausted
	}
	if page.Descriptive != nil {
		out["descriptive"] = descriptiveWire(page.Descriptive)
	}
	send(w, 200, out)
}

func containsType(types []frontend.MediaType, t frontend.MediaType) bool {
	for _, x := range types {
		if x == t {
			return true
		}
	}
	return false
}
