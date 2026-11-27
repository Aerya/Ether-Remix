package experience

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

const (
	// unifiedRetrySeconds is when a pending unified search asks again.
	unifiedRetrySeconds = 1
	// unifiedSettle is how long a described query must stand (asked again
	// by the same client) before it starts an AI job, so every prefix of a
	// query typed word by word does not start one. unifiedSettleExpiry
	// forgets a query the client stopped asking about.
	unifiedSettle       = 400 * time.Millisecond
	unifiedSettleExpiry = 30 * time.Second
	unifiedSettleMax    = 4096
)

// unifiedSettleState remembers, per viewer and client, the described
// query last seen and when.
type unifiedSettleState struct {
	mu   sync.Mutex
	seen map[descriptiveFlightKey]unifiedSeen
}

type unifiedSeen struct {
	query string
	at    time.Time
}

// unifiedNamedScore is the catalogue relevance from which a result shows
// the viewer is naming a title rather than describing one: the title begins
// with the query, its words start the title in order, one spelling
// correction away, a franchise's name being typed, or a person's full name.
// Loose word matches in any order score less (0.72 plus a popularity prior
// of at most 0.10). pluginNamedScore is the same on a plugin library's own
// scale (a whole-title prefix).
const (
	unifiedNamedScore = 0.83
	pluginNamedScore  = 0.90
)

// namesTitle reports whether a result shows the viewer naming a title: it
// scores unifiedNamedScore, or its title holds every word of the query (the
// last one possibly half typed), as "El Camino: A Breaking Bad Movie" holds
// "breaking bad". A description's words are rarely all in one title.
func namesTitle(query string, items []core.Title) bool {
	words := foldWords(query)
	for i := range items {
		if items[i].SearchScore >= unifiedNamedScore {
			return true
		}
		if len(words) > 0 && (holdsWords(foldWords(items[i].Title), words) || holdsWords(foldWords(items[i].OriginalTitle), words)) {
			return true
		}
	}
	return false
}

func foldWords(s string) []string { return core.FoldSearchWords(s) }

func holdsWords(title, words []string) bool {
	if len(title) == 0 {
		return false
	}
	if strings.Contains(strings.Join(title, ""), strings.Join(words, "")) {
		return true
	}
	for i, w := range words {
		found := false
		for _, t := range title {
			if t == w || (i == len(words)-1 && strings.HasPrefix(t, w)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

var unifiedSettles = &unifiedSettleState{seen: map[descriptiveFlightKey]unifiedSeen{}}

// unifiedNow is the clock the settle reads (tests move it).
var unifiedNow = time.Now

// settled records query for (viewer, client) and reports whether the same
// query was already asked at least unifiedSettle ago.
func (u *unifiedSettleState) settled(viewer *core.User, client, query string, now time.Time) bool {
	key := descriptiveFlightKey{viewer: searchViewerID(viewer), client: client}
	norm := normalizeSearchQuery(query)
	u.mu.Lock()
	defer u.mu.Unlock()
	prev, ok := u.seen[key]
	if ok && prev.query == norm && now.Sub(prev.at) < unifiedSettleExpiry {
		return now.Sub(prev.at) >= unifiedSettle
	}
	if len(u.seen) >= unifiedSettleMax {
		for k, e := range u.seen {
			if now.Sub(e.at) >= unifiedSettleExpiry {
				delete(u.seen, k)
			}
		}
		if len(u.seen) >= unifiedSettleMax {
			clear(u.seen)
		}
	}
	u.seen[key] = unifiedSeen{query: norm, at: now}
	return false
}

// UnifiedSearch implements frontend.UnifiedSearcher: one ranked list for a
// search box. Title, franchise, person and fuzzy matches come from Search,
// which never waits on a provider or a plugin's remote catalogue. When AI
// is configured and no result shows the viewer naming a title (namesTitle;
// "james bo" while typing names the Bond films), a query of two or more
// words, or one that matched nothing, is a description: the Collections Describe
// job's titles lead the list, its title matches follow. The job never runs
// inside the request: a described query asked again after unifiedSettle
// starts it, and the response stays Pending until it answers.
func (s *Service) UnifiedSearch(ctx context.Context, viewer *core.Viewer, device core.Device, query string, q frontend.ItemQuery) (frontend.UnifiedSearchPage, error) {
	ctx = core.WithCatalogViewer(core.WithExplicitSearch(ctx), viewer)
	if core.SearchClientFrom(ctx) == "" && device.ID != "" {
		ctx = core.WithSearchClient(ctx, device.ID)
	}
	if viewer != nil {
		var leave func()
		ctx, leave = core.EnterSearch(ctx, viewer.ID, core.SearchClientFrom(ctx), query)
		defer leave()
	}
	page, confident, err := s.search(ctx, viewer, device, query, q, true)
	if err != nil {
		return frontend.UnifiedSearchPage{}, err
	}
	if core.SearchWasSuperseded(ctx) {
		// The viewer typed on: no description job for a stale query.
		return frontend.UnifiedSearchPage{SearchPage: page, Match: frontend.SearchMatchNone}, nil
	}
	out := frontend.UnifiedSearchPage{SearchPage: page, Match: frontend.SearchMatchTitle}
	if len(page.Page.Items) == 0 {
		out.Match = frontend.SearchMatchNone
	}
	if page.Pending {
		out.RetryAfterSeconds = unifiedRetrySeconds
	}
	source := s.descriptiveSource()
	ctrl, _ := source.(core.DescriptiveSearchController)
	client := core.SearchClientFrom(ctx)
	// Any other query from this client, a title or a prefix being typed
	// included, drops the Describe job of the one before: nobody waits on it.
	if prev, superseded := s.ensureSearchGate().supersede(viewer, client, query); superseded && ctrl != nil {
		ctrl.CancelDescribeSearch(viewer, prev)
	}
	// A one- or two-letter prefix is still being typed: never AI.
	if confident || core.IsShortSearch(query) || source == nil || !source.DescriptiveSearchAvailable() || max(q.Offset, 0) > 0 {
		return out, nil
	}
	if len(strings.Fields(query)) < 2 && len(page.Page.Items) > 0 {
		return out, nil
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return frontend.UnifiedSearchPage{}, err
	}
	if !unifiedSettles.settled(v, client, query, unifiedNow()) && (ctrl == nil || !ctrl.DescribeCached(v, query)) {
		// Not asked again yet: the viewer may still be typing.
		out.Pending = true
		out.RetryAfterSeconds = unifiedRetrySeconds
		out.Descriptive = &frontend.DescriptiveSearchStatus{State: frontend.DescriptivePending, RetryAfterSeconds: unifiedRetrySeconds}
		return out, nil
	}
	described, err := s.DescriptiveSearch(ctx, viewer, device, query, q)
	if err != nil {
		// No description match: the title results stand.
		return out, nil
	}
	out.Descriptive = described.Descriptive
	if st := described.Descriptive; st != nil {
		switch st.State {
		case frontend.DescriptivePending, frontend.DescriptivePartial, frontend.DescriptiveBusy:
			out.Pending = true
			out.RetryAfterSeconds = max(unifiedRetrySeconds, st.RetryAfterSeconds)
		}
	}
	if len(described.Page.Items) == 0 {
		return out, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	out.Described = make(map[core.RatingKey]bool, len(described.Page.Items))
	for _, t := range described.Page.Items {
		out.Described[t.Key] = true
	}
	titles := page.Page.Items
	merged := mergeAIAndKeyword(described.Page.Items, titles)
	if len(merged) > limit {
		merged = merged[:limit]
	}
	out.Page.Items = merged
	out.Match = frontend.SearchMatchDescription
	for _, t := range merged {
		if !out.Described[t.Key] {
			out.Match = frontend.SearchMatchMixed
			break
		}
	}
	// A described list is one page: its order is the AI's, not a window.
	out.Continuation = frontend.ContinuationExhausted
	if out.Pending {
		out.Continuation = frontend.ContinuationUnknown
	}
	return out, nil
}

var _ frontend.UnifiedSearcher = (*Service)(nil)
