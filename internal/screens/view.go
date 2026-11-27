package screens

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// Display-ready view models (docs/plugin-screens.md, Display-ready view
// models). Ether sends facts: enums, numbers, names and flags. Documents
// compose sentences from their own strings; Vela computes only what depends
// on the viewer's clock, locale or skin.

// EventView is the value of an event:<id> record.
type EventView struct {
	ID        string     `json:"id"`
	Feed      string     `json:"feed"`
	Category  string     `json:"category,omitempty"`
	Phase     string     `json:"phase"`
	State     string     `json:"state"`
	Detail    string     `json:"detail,omitempty"`
	Clock     string     `json:"clock,omitempty"`
	Period    int        `json:"period,omitempty"`
	Spoiler   bool       `json:"spoiler"`
	Close     bool       `json:"close"`
	Scored    bool       `json:"scored"`
	Favourite bool       `json:"favourite"`
	Start     string     `json:"start"`
	League    LeagueView `json:"league"`
	Title     string     `json:"title,omitempty"`
	Venue     string     `json:"venue,omitempty"`
	Sides     []SideView `json:"sides,omitempty"`
	// PossessionLabel is the sport's own word for the possession line moved
	// into sides[].possession ("Possession", "Serving", "Batting").
	PossessionLabel string           `json:"possessionLabel,omitempty"`
	Ranking         []RankView       `json:"ranking,omitempty"`
	Situation       []LabelValue     `json:"situation"`
	Probability     *ProbabilityView `json:"probability,omitempty"`
	Watch           WatchView        `json:"watch"`
	UpdatedAt       string           `json:"updatedAt,omitempty"`
}

// DetailView is the value of a detail:<event id> record: what only the game
// page needs. It is a separate record so the summary event:<id> record keeps
// one shape everywhere.
type DetailView struct {
	ID                string                           `json:"id"`
	Plays             []PlayView                       `json:"plays"`
	PlaysNewest       []PlayView                       `json:"playsNewest"`
	Tables            []TableView                      `json:"tables"`
	ProbabilitySeries []core.LiveEventProbabilityPoint `json:"probabilitySeries"`
	Links             []map[string]string              `json:"links"`
}

// PlayView is a play with its score projected into the sides' reading
// order, so a document reads scores.0 and scores.1.
type PlayView struct {
	core.LiveEventPlay
	Scores []string `json:"scores,omitempty"`
}

func playViews(plays []core.LiveEventPlay, competitors []core.LiveEventCompetitor) []PlayView {
	out := make([]PlayView, 0, len(plays))
	for _, p := range plays {
		v := PlayView{LiveEventPlay: p}
		if len(p.Score) > 0 && len(competitors) == 2 {
			a, aok := p.Score[competitors[0].ID]
			b, bok := p.Score[competitors[1].ID]
			if aok && bok {
				v.Scores = []string{a, b}
			}
		}
		out = append(out, v)
	}
	return out
}

// LeagueView names a league; Label is the agreed chip text.
type LeagueView struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Abbr  string `json:"abbr,omitempty"`
	Label string `json:"label"`
	Logo  string `json:"logo,omitempty"`
}

// SideView is one of two competitors, in the plugin's reading order.
type SideView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Short     string `json:"short"`
	Abbr      string `json:"abbr"`
	Logo      string `json:"logo,omitempty"`
	Color     string `json:"color,omitempty"`
	AltColor  string `json:"altColor,omitempty"`
	Score     string `json:"score"`
	Record    string `json:"record,omitempty"`
	EntityID  string `json:"entityId,omitempty"`
	Favourite bool   `json:"favourite"`
	Winner    bool   `json:"winner"`
	Loser     bool   `json:"loser"`
	Share     *int   `json:"share,omitempty"`
	Leads     bool   `json:"leads,omitempty"`
	// Possession marks the side with the ball, serve or bat, read from the
	// plugin's situation (that line then leaves situation).
	Possession bool `json:"possession,omitempty"`
}

// RankView is a leaderboard row.
type RankView struct {
	Place     string `json:"place"`
	Pos       int    `json:"pos"`
	Name      string `json:"name"`
	Abbr      string `json:"abbr,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Secondary string `json:"secondary,omitempty"`
	EntityID  string `json:"entityId,omitempty"`
	Favourite bool   `json:"favourite"`
}

// LabelValue is one situation line.
type LabelValue struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ProbabilityView is the two-sided win probability.
type ProbabilityView struct {
	Home        float64 `json:"home"`
	Away        float64 `json:"away"`
	SubjectSide int     `json:"subjectSide"`
	Leader      struct {
		Abbr  string `json:"abbr"`
		Share int    `json:"share"`
	} `json:"leader"`
}

// WatchView is where to watch: tunable targets first, then what airs it.
type WatchView struct {
	Targets []WatchTargetView `json:"targets"`
	Primary *WatchTargetView  `json:"primary,omitempty"`
	Airing  []AiringView      `json:"airing"`
}

// WatchTargetView is one tunable place to watch.
type WatchTargetView struct {
	Kind      string `json:"kind"`
	ChannelID string `json:"channelId"`
	Name      string `json:"name"`
	Number    string `json:"number,omitempty"`
	Logo      string `json:"logo,omitempty"`
	Live      bool   `json:"live"`
}

// AiringView names where an event airs without being an action.
type AiringView struct {
	Name        string `json:"name"`
	Unavailable bool   `json:"unavailable,omitempty"`
}

// TableView is a generic table; a row about a viewer's favourite entity is
// highlighted.
type TableView struct {
	Title   string    `json:"title"`
	Columns []string  `json:"columns"`
	Rows    []RowView `json:"rows"`
}

// RowView is one table row.
type RowView struct {
	Cells     []string `json:"cells"`
	Highlight bool     `json:"highlight,omitempty"`
	EntityID  string   `json:"entityId,omitempty"`
}

// EntityView is the value of an entity:<key> record.
type EntityView struct {
	Key       string `json:"key"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Abbr      string `json:"abbr,omitempty"`
	Logo      string `json:"logo,omitempty"`
	League    string `json:"league,omitempty"`
	Color     string `json:"color,omitempty"`
	Favourite bool   `json:"favourite"`
}

// favourites is the viewer's favourite entities.
type favourites = *favoriteSet

// entity reports whether the entity is one of the viewer's favourites. An id
// alone matches within the feed when the plugin's entity kind is unknown.
func (f *favoriteSet) entity(pluginID, feed, kind, id string) bool {
	if f == nil || id == "" {
		return false
	}
	if f.full[pluginID+":"+feed+":"+kind+":"+id] {
		return true
	}
	m := f.byFeed[pluginID+":"+feed]
	return m != nil && (m[kind+":"+id] || (kind == "" && m[id]))
}

// phaseOf maps a status state to the agreed phase.
func phaseOf(state string) string {
	switch state {
	case core.LiveEventStateIn:
		return "live"
	case core.LiveEventStatePre, core.LiveEventStateDelayed:
		return "upcoming"
	case core.LiveEventStatePost:
		return "final"
	default:
		return "off"
	}
}

var hexColourRe = regexp.MustCompile(`^#?([0-9a-fA-F]{6})$`)

func normColour(c string) string {
	m := hexColourRe.FindStringSubmatch(strings.TrimSpace(c))
	if m == nil {
		return ""
	}
	return "#" + strings.ToLower(m[1])
}

// initials gives up to three letters for a crest with no abbreviation.
func initials(name string) string {
	words := strings.Fields(name)
	if len(words) > 1 {
		var b strings.Builder
		for i, w := range words {
			if i == 3 {
				break
			}
			b.WriteString(strings.ToUpper(string([]rune(w)[:1])))
		}
		return b.String()
	}
	if len(words) == 0 {
		return ""
	}
	r := []rune(words[0])
	if len(r) > 3 {
		r = r[:3]
	}
	return strings.ToUpper(string(r))
}

// leagueLabel is the agreed chip text for a league.
func leagueLabel(name, abbr string) string {
	name, abbr = strings.TrimSpace(name), strings.TrimSpace(abbr)
	if abbr == "" || name == "" || strings.EqualFold(abbr, name) {
		if name != "" {
			return name
		}
		return abbr
	}
	words := strings.Fields(name)
	if strings.EqualFold(words[0], abbr) {
		return abbr
	}
	var ini strings.Builder
	for _, w := range words {
		if _, err := strconv.Atoi(w); err == nil {
			ini.WriteString(w)
		} else {
			ini.WriteString(string([]rune(w)[:1]))
		}
	}
	if strings.EqualFold(ini.String(), abbr) && (len([]rune(abbr)) >= 3 || len([]rune(name)) > 8) {
		return abbr
	}
	if len([]rune(name)) <= 12 {
		return name
	}
	return abbr
}

func league(l core.LiveEventLeague) LeagueView {
	return LeagueView{ID: l.ID, Name: l.Name, Abbr: l.Abbr, Label: leagueLabel(l.Name, l.Abbr), Logo: l.Logo}
}

func num(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil
}

func side(c, other core.LiveEventCompetitor, final bool, favs favourites, pluginID, feed, entityKind string) SideView {
	short := c.Short
	if short == "" {
		short = c.Name
	}
	abbr := c.Abbr
	if abbr == "" {
		abbr = initials(short)
	}
	s := SideView{ID: c.ID, Name: c.Name, Short: short, Abbr: abbr, Logo: c.Logo, Color: normColour(c.Color), AltColor: normColour(c.AltColor),
		Score: c.Score, Record: c.Record, EntityID: c.EntityID, Favourite: favs.entity(pluginID, feed, entityKind, c.EntityID)}
	if final {
		a, aok := num(c.Score)
		b, bok := num(other.Score)
		flagged := c.Winner || other.Winner
		s.Loser = (!c.Winner && other.Winner) || (!flagged && aok && bok && a < b)
		s.Winner = c.Winner || (!s.Loser && !flagged && aok && bok && a > b)
	}
	return s
}

// buildEventView derives the summary record value for ev.
func buildEventView(ev core.LiveEvent, favs favourites, entityKind string) EventView {
	pluginID, feed := eventPluginFeed(ev)
	phase := phaseOf(ev.Status.State)
	v := EventView{
		ID: ev.ID, Feed: pluginID + ":" + feed, Category: ev.Category, Phase: phase, State: ev.Status.State,
		Detail: ev.Status.Detail, Clock: ev.Status.Clock, Period: ev.Status.Period, Spoiler: ev.Spoiler,
		Close: !ev.Spoiler && ev.Status.Close, Start: ev.Start, League: league(ev.League), Title: ev.Title,
		Venue: ev.Venue, UpdatedAt: ev.UpdatedAt, Situation: []LabelValue{},
	}
	if len(ev.Competitors) == 2 {
		final := phase == "final"
		a := side(ev.Competitors[0], ev.Competitors[1], final, favs, pluginID, feed, entityKind)
		b := side(ev.Competitors[1], ev.Competitors[0], final, favs, pluginID, feed, entityKind)
		v.Sides = []SideView{a, b}
		v.Favourite = a.Favourite || b.Favourite
		v.Scored = !ev.Spoiler && phase != "upcoming" && a.Score != "" && b.Score != ""
		if p := ev.Probability; p != nil && !ev.Spoiler {
			val := math.Max(0, math.Min(1, p.Value))
			subjectSide := 0
			if p.CompetitorID != "" && p.CompetitorID == ev.Competitors[1].ID {
				subjectSide = 1
			}
			// The home side is the one the plugin flags, else the second
			// side in reading order (away at home).
			homeIdx := 1
			if ev.Competitors[0].Home && !ev.Competitors[1].Home {
				homeIdx = 0
			}
			shares := [2]float64{}
			shares[subjectSide] = val
			shares[1-subjectSide] = 1 - val
			pv := &ProbabilityView{Home: round3(shares[homeIdx]), Away: round3(shares[1-homeIdx]), SubjectSide: subjectSide}
			pct := [2]int{int(math.Round(shares[0] * 100)), 0}
			pct[1] = 100 - pct[0]
			lead := 0
			if pct[1] > pct[0] {
				lead = 1
			}
			pv.Leader.Abbr, pv.Leader.Share = v.Sides[lead].Abbr, pct[lead]
			v.Sides[0].Share, v.Sides[1].Share = &pct[0], &pct[1]
			v.Sides[0].Leads, v.Sides[1].Leads = pct[0] > pct[1], pct[1] > pct[0]
			v.Probability = pv
		}
	}
	if len(ev.Ranking) > 0 {
		counts := map[int]int{}
		for _, r := range ev.Ranking {
			counts[r.Pos]++
		}
		for _, r := range ev.Ranking {
			place := strconv.Itoa(r.Pos)
			if counts[r.Pos] > 1 {
				place = "T" + place
			}
			v.Ranking = append(v.Ranking, RankView{Place: place, Pos: r.Pos, Name: r.Name, Abbr: r.Abbr, Detail: strings.TrimSpace(r.Detail),
				Secondary: strings.TrimSpace(r.Secondary), EntityID: r.EntityID, Favourite: favs.entity(pluginID, feed, entityKind, r.EntityID)})
		}
		for _, r := range v.Ranking {
			if r.Favourite {
				v.Favourite = true
			}
		}
	}
	possessionKey := ""
	if !ev.Spoiler && len(v.Sides) == 2 {
		for k, val := range ev.Situation {
			if !possessionRe.MatchString(strings.TrimSpace(k)) {
				continue
			}
			for i := range v.Sides {
				if namesSide(val, v.Sides[i]) {
					v.Sides[i].Possession = true
					possessionKey = k
					v.PossessionLabel = strings.TrimSpace(k)
				}
			}
			if possessionKey != "" {
				break
			}
		}
	}
	if !ev.Spoiler {
		shown := map[string]bool{strings.TrimSpace(ev.Status.Detail): true, strings.TrimSpace(ev.Status.Clock): true}
		keys := make([]string, 0, len(ev.Situation))
		for k := range ev.Situation {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == possessionKey {
				continue
			}
			val := strings.TrimSpace(ev.Situation[k])
			if val == "" || shown[val] {
				continue
			}
			v.Situation = append(v.Situation, LabelValue{Label: k, Value: val})
		}
	}
	v.Watch = watchView(ev)
	return v
}

// buildDetailView derives the detail:<id> record value. Under a spoiler the
// hub has already withheld plays and the series.
func buildDetailView(d core.LiveEventDetail, favs favourites, entityKind string) DetailView {
	pluginID, feed := eventPluginFeed(d.LiveEvent)
	v := DetailView{ID: d.ID, Plays: []PlayView{}, PlaysNewest: []PlayView{}, Tables: []TableView{},
		ProbabilitySeries: []core.LiveEventProbabilityPoint{}, Links: []map[string]string{}}
	if !d.Spoiler {
		if len(d.Plays) > 0 {
			v.Plays = playViews(d.Plays, d.Competitors)
			v.PlaysNewest = playViews(newestFirst(d.Plays), d.Competitors)
		}
		if len(d.ProbabilitySeries) > 0 {
			v.ProbabilitySeries = d.ProbabilitySeries
		}
	}
	for _, t := range d.Tables {
		v.Tables = append(v.Tables, tableView(t, favs, pluginID, feed, entityKind))
	}
	for _, l := range d.Links {
		v.Links = append(v.Links, map[string]string{"label": l.Label, "url": l.URL})
	}
	return v
}

// tableView converts a generic table, marking rows about favourites.
func tableView(t core.LiveEventTable, favs favourites, pluginID, feed, entityKind string) TableView {
	tv := TableView{Title: t.Title, Columns: t.Columns, Rows: make([]RowView, 0, len(t.Rows))}
	if tv.Columns == nil {
		tv.Columns = []string{}
	}
	withIDs := len(t.RowEntityIDs) == len(t.Rows)
	for i, row := range t.Rows {
		rv := RowView{Cells: row}
		if rv.Cells == nil {
			rv.Cells = []string{}
		}
		if withIDs && t.RowEntityIDs[i] != "" {
			rv.EntityID = t.RowEntityIDs[i]
			rv.Highlight = favs.entity(pluginID, feed, entityKind, rv.EntityID)
		}
		tv.Rows = append(tv.Rows, rv)
	}
	return tv
}

var possessionRe = regexp.MustCompile(`(?i)^(possession|ball|serving|serve|batting|at bat)$`)

// namesSide reports whether a situation value names side: its abbreviation,
// short name, name or id, alone or leading the value ("PHI ball").
func namesSide(value string, side SideView) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return false
	}
	first := strings.Fields(v)[0]
	for _, n := range []string{side.Abbr, side.Short, side.Name, side.ID} {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" && (v == n || first == n) {
			return true
		}
	}
	return false
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

// newestFirst reverses oldest-first plays unless their periods already fall.
func newestFirst(plays []core.LiveEventPlay) []core.LiveEventPlay {
	out := append([]core.LiveEventPlay(nil), plays...)
	if len(out) < 2 {
		return out
	}
	if out[0].Period > out[len(out)-1].Period && out[len(out)-1].Period != 0 {
		return out
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func watchKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func watchView(ev core.LiveEvent) WatchView {
	w := WatchView{Targets: []WatchTargetView{}, Airing: []AiringView{}}
	type scored struct {
		t     core.LiveEventWatchTarget
		score float64
	}
	var tunable []scored
	for _, t := range ev.Watch {
		if t.ChannelID == "" || !t.IsAvailable() {
			continue
		}
		s := t.Confidence
		if t.Live {
			s += 2
		}
		if t.Kind == "channel" {
			s++
		}
		tunable = append(tunable, scored{t, s})
	}
	sort.SliceStable(tunable, func(i, j int) bool { return tunable[i].score > tunable[j].score })
	tuned := map[string]bool{}
	for _, s := range tunable {
		w.Targets = append(w.Targets, WatchTargetView{Kind: s.t.Kind, ChannelID: s.t.ChannelID, Name: s.t.Name, Number: s.t.Number, Logo: s.t.Logo, Live: s.t.Live})
		tuned[watchKey(s.t.Name)] = true
	}
	if len(w.Targets) > 0 {
		p := w.Targets[0]
		w.Primary = &p
	}
	var names []AiringView
	for _, t := range ev.Watch {
		if t.ChannelID != "" && !t.IsAvailable() {
			name := t.Name
			if t.Number != "" {
				name = t.Name + " · " + t.Number
			}
			names = append(names, AiringView{Name: name, Unavailable: true})
		}
	}
	for _, t := range ev.Watch {
		if t.Kind == "addon" && (t.ChannelID == "" || !t.IsAvailable()) && t.ChannelID == "" {
			names = append(names, AiringView{Name: t.Name})
		}
	}
	rank := map[string]int{"tv": 0, "stream": 1, "radio": 2}
	bc := append([]core.LiveEventBroadcast(nil), ev.Broadcasts...)
	sort.SliceStable(bc, func(i, j int) bool {
		ri, ok := rank[bc[i].Kind]
		if !ok {
			ri = 3
		}
		rj, ok := rank[bc[j].Kind]
		if !ok {
			rj = 3
		}
		return ri < rj
	})
	for _, b := range bc {
		names = append(names, AiringView{Name: b.Name})
	}
	seen := map[string]bool{}
	for _, n := range names {
		n.Name = strings.TrimSpace(n.Name)
		k := watchKey(n.Name)
		if n.Name == "" || seen[k] || (tuned[k] && !n.Unavailable) {
			continue
		}
		seen[k] = true
		w.Airing = append(w.Airing, n)
		if len(w.Airing) == 2 {
			break
		}
	}
	return w
}

func eventPluginFeed(ev core.LiveEvent) (string, string) {
	parts := strings.SplitN(ev.ID, ":", 3)
	if len(parts) == 3 {
		return parts[0], parts[1]
	}
	return "", ev.Feed
}

func entityView(e core.LiveEventEntity, pluginID, feed string, favs favourites) EntityView {
	key := pluginID + ":" + feed + ":" + e.Kind + ":" + e.ID
	return EntityView{Key: key, ID: e.ID, Name: e.Name, Abbr: e.Abbr, Logo: e.Logo, League: e.League, Color: normColour(e.Color), Favourite: favs.hasKey(key)}
}
