package diagnostics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Bounds for the release-search collector.
const (
	maxSearchAttempts     = 16
	maxSearchReleases     = 40
	maxSearchQueries      = 48
	maxRejectionReasons   = 12
	maxRejectedNamesShown = 5
)

// ReleaseSearchQuery selects the resolutions a capture explains: those the
// captured device (or, when a resolution has no device, the viewer) caused
// inside the window.
type ReleaseSearchQuery struct {
	From      time.Time
	Until     time.Time
	ViewerID  int64
	DeviceKey string
}

// ReleaseSearchInput is what the resolver recorded, before redaction.
// IndexerNames lists every configured indexer name so free text that
// mentions one can be scrubbed; names never leave this package.
type ReleaseSearchInput struct {
	Attempts     []ResolverAttempt
	IndexerNames []string
}

// ResolverAttempt is one resolution. Indexer fields hold the owner's
// indexer names; the collector replaces them with letters.
type ResolverAttempt struct {
	At                time.Time
	Title             string
	Speculative       bool
	Profile           string
	Discovery         []string
	Queries           []ResolverQuery
	QueriesDropped    int
	Candidates        []ResolverCandidate
	CandidatesOmitted int
	Winner            string
	Error             string
	SearchDuration    time.Duration
	TotalDuration     time.Duration
	// Earlier is the last real search behind a remembered answer.
	Earlier *ResolverAttempt
}

// ResolverQuery is one indexer request.
type ResolverQuery struct {
	Indexer string
	Kind    string
	Mode    string
	Query   string
	Season  string
	Episode string
	IDs     string
	Results int
	Error   string
	Rescue  bool
}

// ResolverCandidate is one release the resolver ranked.
type ResolverCandidate struct {
	Name      string
	Indexer   string
	Height    int
	Size      int64
	Rejected  bool
	Reason    string
	Attempted bool
	Healthy   bool
	Chosen    bool
}

// Exported shape of release-search.json.
type releaseSearchDoc struct {
	Note      string                 `json:"note"`
	Indexers  []releaseSearchIndexer `json:"indexers"`
	Attempts  []releaseSearchAttempt `json:"attempts"`
	Truncated bool                   `json:"truncated,omitempty"`
}

type releaseSearchIndexer struct {
	Label string `json:"label"`
	Kind  string `json:"kind,omitempty"`
}

type releaseSearchAttempt struct {
	At                 time.Time                `json:"at"`
	Title              string                   `json:"title,omitempty"`
	Purpose            string                   `json:"purpose"`
	Outcome            string                   `json:"outcome"`
	ChosenRelease      string                   `json:"chosenRelease,omitempty"`
	Error              string                   `json:"error,omitempty"`
	Profile            string                   `json:"profile,omitempty"`
	SearchMS           int64                    `json:"searchMs"`
	TotalMS            int64                    `json:"totalMs"`
	Discovery          []string                 `json:"discovery,omitempty"`
	IndexersQueried    []string                 `json:"indexersQueried,omitempty"`
	Queries            []releaseSearchQuery     `json:"queries,omitempty"`
	QueriesOmitted     int                      `json:"queriesOmitted,omitempty"`
	ReleasesConsidered int                      `json:"releasesConsidered"`
	Releases           []releaseSearchRelease   `json:"releases,omitempty"`
	ReleasesOmitted    int                      `json:"releasesOmitted,omitempty"`
	Rejections         []releaseSearchRejection `json:"rejections,omitempty"`
	EarlierSearch      *releaseSearchAttempt    `json:"earlierSearch,omitempty"`
}

type releaseSearchQuery struct {
	Indexer string `json:"indexer"`
	Mode    string `json:"mode,omitempty"`
	Query   string `json:"query,omitempty"`
	Season  string `json:"season,omitempty"`
	Episode string `json:"episode,omitempty"`
	IDs     string `json:"ids,omitempty"`
	Results int    `json:"results"`
	Error   string `json:"error,omitempty"`
	Rescue  bool   `json:"rescue,omitempty"`
}

type releaseSearchRelease struct {
	Name     string `json:"name"`
	Indexer  string `json:"indexer,omitempty"`
	Height   int    `json:"height,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Rejected bool   `json:"rejected,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Tried    bool   `json:"tried,omitempty"`
	Healthy  bool   `json:"healthy,omitempty"`
	Chosen   bool   `json:"chosen,omitempty"`
}

type releaseSearchRejection struct {
	Reason string   `json:"reason"`
	Count  int      `json:"count"`
	Top    []string `json:"top,omitempty"`
}

// releaseSearchExport anonymises and bounds the recorded resolutions.
// Indexers become "indexer A", "indexer B"… in order of first appearance.
// Without the titles flag, titles and query text are dropped and release
// names become neutral tokens.
type releaseSearchExport struct {
	titles  bool
	uses    []secretUse
	names   []string // configured indexer names, longest first
	labels  map[string]string
	kinds   map[string]string
	order   []string
	release map[string]string
}

func releaseSearchDocument(in ReleaseSearchInput, titles bool, uses []secretUse) releaseSearchDoc {
	x := &releaseSearchExport{titles: titles, uses: uses, labels: map[string]string{}, kinds: map[string]string{}, release: map[string]string{}}
	for _, n := range in.IndexerNames {
		// Very short names would rewrite ordinary words in reasons.
		if n = strings.TrimSpace(n); len(n) >= 3 {
			x.names = append(x.names, n)
		}
	}
	sort.SliceStable(x.names, func(i, j int) bool { return len(x.names[i]) > len(x.names[j]) })
	doc := releaseSearchDoc{
		Note: "Resolutions the recorded device caused during the capture, from the resolver history. Indexers are labelled by letter; their names and addresses are not included.",
	}
	attempts := in.Attempts
	if len(attempts) > maxSearchAttempts {
		// Prefer play attempts, then the newest.
		var play, spec []ResolverAttempt
		for _, a := range attempts {
			if a.Speculative {
				spec = append(spec, a)
			} else {
				play = append(play, a)
			}
		}
		if len(play) > maxSearchAttempts {
			play = play[len(play)-maxSearchAttempts:]
		}
		if room := maxSearchAttempts - len(play); room < len(spec) {
			spec = spec[len(spec)-room:]
		}
		attempts = append(play, spec...)
		sort.SliceStable(attempts, func(i, j int) bool { return attempts[i].At.Before(attempts[j].At) })
		doc.Truncated = true
	}
	shownEarlier := map[time.Time]bool{}
	for _, a := range attempts {
		// Several retries share one remembered answer: show its search once.
		withEarlier := a.Earlier != nil && !shownEarlier[a.Earlier.At]
		if withEarlier {
			shownEarlier[a.Earlier.At] = true
		}
		doc.Attempts = append(doc.Attempts, x.attempt(a, withEarlier))
	}
	for _, name := range x.order {
		doc.Indexers = append(doc.Indexers, releaseSearchIndexer{Label: x.labels[name], Kind: x.kinds[name]})
	}
	return doc
}

func (x *releaseSearchExport) label(name, kind string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return "indexer ?"
	}
	if l, ok := x.labels[key]; ok {
		if x.kinds[key] == "" {
			x.kinds[key] = clipFree(kind, 40, x.uses)
		}
		return l
	}
	n := len(x.order)
	l := "indexer " + letterLabel(n)
	x.labels[key] = l
	x.kinds[key] = clipFree(kind, 40, x.uses)
	x.order = append(x.order, key)
	return l
}

func letterLabel(n int) string {
	s := ""
	for {
		s = string(rune('A'+n%26)) + s
		n = n/26 - 1
		if n < 0 {
			return s
		}
	}
}

// scrub removes indexer names and network text from free text.
func (x *releaseSearchExport) scrub(s string, max int) string {
	if s == "" {
		return ""
	}
	s = networkInput(s)
	for _, n := range x.names {
		s = replaceFold(s, n, x.label(n, ""))
	}
	return clipNetwork(s, max, x.uses)
}

func replaceFold(s, needle, with string) string {
	if needle == "" {
		return s
	}
	lower, ln := strings.ToLower(s), strings.ToLower(needle)
	var b strings.Builder
	for {
		i := strings.Index(lower, ln)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(with)
		s, lower = s[i+len(ln):], lower[i+len(ln):]
	}
}

// clipText bounds title text (release names, query strings). Dotted
// release names look like host names to the network redaction ("…Home.Alone"
// ends in a home-network suffix), so only URLs and addresses are removed;
// an indexer name inside a release name (a release group named after its
// indexer) is still replaced.
func (x *releaseSearchExport) clipText(s string, max int) string {
	s = networkInput(s)
	for _, n := range x.names {
		s = replaceFold(s, n, x.label(n, ""))
	}
	return clipFree(supportIPText.ReplaceAllString(s, redactMask), max, x.uses)
}

func (x *releaseSearchExport) releaseName(name string) string {
	if x.titles {
		return x.clipText(name, maxString)
	}
	if tok, ok := x.release[name]; ok {
		return tok
	}
	tok := fmt.Sprintf("release %d", len(x.release)+1)
	x.release[name] = tok
	return tok
}

func (x *releaseSearchExport) attempt(a ResolverAttempt, withEarlier bool) releaseSearchAttempt {
	out := releaseSearchAttempt{
		At:       a.At.UTC(),
		Purpose:  "play",
		Profile:  clipNetwork(a.Profile, 80, x.uses),
		SearchMS: a.SearchDuration.Milliseconds(),
		TotalMS:  a.TotalDuration.Milliseconds(),
		Error:    x.scrub(a.Error, maxExcerpt),
	}
	if a.Speculative {
		out.Purpose = "warm-up"
	}
	if x.titles {
		out.Title = x.clipText(a.Title, maxString)
	}
	for _, d := range a.Discovery {
		out.Discovery = append(out.Discovery, clipFree(d, 80, x.uses))
	}
	queried := map[string]bool{}
	queries := a.Queries
	if len(queries) > maxSearchQueries {
		out.QueriesOmitted = len(queries) - maxSearchQueries
		queries = queries[:maxSearchQueries]
	}
	out.QueriesOmitted += a.QueriesDropped
	for _, q := range queries {
		l := x.label(q.Indexer, q.Kind)
		if !queried[l] {
			queried[l] = true
			kind := x.kinds[strings.ToLower(strings.TrimSpace(q.Indexer))]
			if kind != "" {
				out.IndexersQueried = append(out.IndexersQueried, l+" ("+kind+")")
			} else {
				out.IndexersQueried = append(out.IndexersQueried, l)
			}
		}
		rq := releaseSearchQuery{
			Indexer: l,
			Mode:    clipFree(q.Mode, 40, x.uses),
			Season:  clipFree(q.Season, 16, x.uses),
			Episode: clipFree(q.Episode, 16, x.uses),
			IDs:     clipNetwork(q.IDs, 120, x.uses),
			Results: q.Results,
			Error:   x.scrub(q.Error, maxString),
			Rescue:  q.Rescue,
		}
		if x.titles {
			rq.Query = x.clipText(q.Query, maxString)
		}
		out.Queries = append(out.Queries, rq)
	}
	out.ReleasesConsidered = len(a.Candidates) + a.CandidatesOmitted
	type group struct {
		reason string
		count  int
		top    []string
	}
	groups := map[string]*group{}
	var order []string
	// List the releases that tell the most first: the chosen one, accepted
	// ones, ones actually tried, then near misses (the right title rejected
	// for episode, year or quality) ahead of other titles.
	rank := func(c ResolverCandidate) int {
		switch {
		case c.Chosen:
			return 0
		case !c.Rejected:
			return 1
		case c.Attempted:
			return 2
		case !strings.Contains(strings.ToLower(c.Reason), "title"):
			return 3
		}
		return 4
	}
	cands := append([]ResolverCandidate(nil), a.Candidates...)
	sort.SliceStable(cands, func(i, j int) bool { return rank(cands[i]) < rank(cands[j]) })
	for i, c := range cands {
		name := x.releaseName(c.Name)
		reason := x.scrub(c.Reason, maxString)
		if c.Chosen {
			out.ChosenRelease = name
		}
		if c.Rejected {
			// "title mismatch (parsed …; expected …)" groups as one reason;
			// the release list keeps the full text.
			key, _, _ := strings.Cut(reason, " (")
			key = strings.TrimSpace(key)
			if key == "" {
				key = "rejected"
			}
			g := groups[key]
			if g == nil {
				g = &group{reason: key}
				groups[key] = g
				order = append(order, key)
			}
			g.count++
			if len(g.top) < maxRejectedNamesShown {
				g.top = append(g.top, name)
			}
		}
		if i >= maxSearchReleases {
			out.ReleasesOmitted++
			continue
		}
		rel := releaseSearchRelease{Name: name, Height: c.Height, Size: c.Size, Rejected: c.Rejected, Reason: reason, Tried: c.Attempted, Healthy: c.Healthy, Chosen: c.Chosen}
		if c.Indexer != "" {
			rel.Indexer = x.label(c.Indexer, "")
		}
		out.Releases = append(out.Releases, rel)
	}
	out.ReleasesOmitted += a.CandidatesOmitted
	sort.SliceStable(order, func(i, j int) bool { return groups[order[i]].count > groups[order[j]].count })
	for i, key := range order {
		if i >= maxRejectionReasons {
			break
		}
		g := groups[key]
		out.Rejections = append(out.Rejections, releaseSearchRejection{Reason: g.reason, Count: g.count, Top: g.top})
	}
	if out.ChosenRelease == "" && a.Winner != "" {
		out.ChosenRelease = x.releaseName(a.Winner)
	}
	switch {
	case out.ChosenRelease != "":
		out.Outcome = "chose a release"
	case strings.Contains(a.Error, "cooloff"):
		out.Outcome = "answered from a remembered \"not available\" result; no new search"
	case len(a.Queries) == 0 && len(a.Candidates) == 0 && a.Error == "":
		out.Outcome = "no search recorded"
	case len(a.Candidates) == 0:
		out.Outcome = "no release found"
	default:
		out.Outcome = "no release accepted"
	}
	if withEarlier && a.Earlier != nil {
		e := x.attempt(*a.Earlier, false)
		out.EarlierSearch = &e
	}
	return out
}

// writeReleaseSearch renders release-search.json for REPORT.md.
func writeReleaseSearch(b *bytes.Buffer, extra []archived) {
	var doc *releaseSearchDoc
	for _, file := range extra {
		if file.name == "release-search.json" {
			var d releaseSearchDoc
			if json.Unmarshal(file.body, &d) == nil {
				doc = &d
			}
			break
		}
	}
	b.WriteString("\n## Release search\n\n")
	if doc == nil {
		b.WriteString("No release search record was available.\n")
		return
	}
	if len(doc.Attempts) == 0 {
		b.WriteString("No release search ran for this device during the recording.\n")
		return
	}
	b.WriteString("Resolutions this device caused during the recording (warm-ups included), from the resolver history. Indexers are labelled by letter; their names and addresses are not included. Full detail is in release-search.json.\n")
	if doc.Truncated {
		b.WriteString("\nOlder warm-ups were left out to keep the report bounded.\n")
	}
	for _, a := range doc.Attempts {
		if b.Len() > 160<<10 {
			b.WriteString("\nRelease search shortened; see release-search.json.\n")
			return
		}
		writeReleaseAttempt(b, a, "###")
		if a.EarlierSearch != nil {
			fmt.Fprintf(b, "\nThe remembered answer came from this earlier search:\n")
			writeReleaseAttempt(b, *a.EarlierSearch, "####")
		}
	}
}

func writeReleaseAttempt(b *bytes.Buffer, a releaseSearchAttempt, heading string) {
	title := a.Title
	if title == "" {
		title = "(title not included)"
	}
	fmt.Fprintf(b, "\n%s %s %s (%s)\n\n", heading, a.At.UTC().Format("15:04:05"), listText(title), a.Purpose)
	if a.Title != "" {
		fmt.Fprintf(b, "  - item: %s\n", strconv.Quote(a.Title))
	}
	fmt.Fprintf(b, "- Outcome: %s", a.Outcome)
	if a.ChosenRelease != "" {
		fmt.Fprintf(b, ": %s", listText(strconv.Quote(a.ChosenRelease)))
	}
	b.WriteString(".")
	if a.Error != "" {
		fmt.Fprintf(b, " Error: %s.", listText(a.Error))
	}
	fmt.Fprintf(b, " Search %s, total %s", reportDuration(a.SearchMS), reportDuration(a.TotalMS))
	if a.Profile != "" {
		fmt.Fprintf(b, ", profile %s", strconv.Quote(a.Profile))
	}
	b.WriteString(".\n")
	if len(a.Discovery) > 0 {
		fmt.Fprintf(b, "- Sources of releases: %s.\n", listText(strings.Join(a.Discovery, ", ")))
	}
	if len(a.IndexersQueried) > 0 {
		fmt.Fprintf(b, "- Indexers queried: %s.\n", listText(strings.Join(a.IndexersQueried, ", ")))
	}
	if len(a.Queries) > 0 {
		b.WriteString("\n| Indexer | Type | Query | Season / episode | IDs | Results | Error |\n| --- | --- | --- | --- | --- | --- | --- |\n")
		for _, q := range a.Queries {
			mode := q.Mode
			if q.Rescue {
				mode += " (rescue)"
			}
			se := ""
			if q.Season != "" || q.Episode != "" {
				se = q.Season + " / " + q.Episode
			}
			fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %d | %s |\n", reportCell(q.Indexer), reportCell(mode), reportCell(q.Query), reportCell(se), reportCell(q.IDs), q.Results, reportCell(q.Error))
		}
		if a.QueriesOmitted > 0 {
			fmt.Fprintf(b, "\n%d more queries are not listed.\n", a.QueriesOmitted)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("- No indexer query was recorded for this resolution.\n")
	}
	fmt.Fprintf(b, "- Releases checked: %d.\n", a.ReleasesConsidered)
	if len(a.Rejections) > 0 {
		b.WriteString("- Rejections:\n")
		for _, r := range a.Rejections {
			fmt.Fprintf(b, "  - %s: %d", listText(r.Reason), r.Count)
			if len(r.Top) > 0 {
				quoted := make([]string, len(r.Top))
				for i, n := range r.Top {
					quoted[i] = listText(strconv.Quote(n))
				}
				fmt.Fprintf(b, " (e.g. %s)", strings.Join(quoted, ", "))
			}
			b.WriteString("\n")
		}
	}
	if len(a.Releases) > 0 {
		b.WriteString("- Releases:\n")
		for _, r := range a.Releases {
			state := "accepted"
			switch {
			case r.Chosen:
				state = "chosen"
			case r.Rejected:
				state = "rejected: " + r.Reason
			}
			extra := ""
			if r.Indexer != "" {
				extra = ", " + r.Indexer
			}
			if r.Height > 0 {
				extra += fmt.Sprintf(", %dp", r.Height)
			}
			if r.Size > 0 {
				extra += ", " + reportBytes(r.Size)
			}
			if r.Tried {
				extra += ", tried"
			}
			fmt.Fprintf(b, "  - %s%s; %s\n", listText(strconv.Quote(r.Name)), extra, listText(state))
		}
		if a.ReleasesOmitted > 0 {
			fmt.Fprintf(b, "  - %d more releases are not listed.\n", a.ReleasesOmitted)
		}
	}
}

// listText escapes captured text for a Markdown list line: no raw HTML,
// no code spans, no line breaks.
func listText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "`", "&#96;", "\n", " ", "\r", " ").Replace(s)
}
