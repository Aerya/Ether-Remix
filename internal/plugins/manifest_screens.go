package plugins

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	// MaxPluginScreens bounds screens declared by one plugin.
	MaxPluginScreens = 8
	// MaxScreenSections bounds sections declared on one screen.
	MaxScreenSections = 20
	// MaxScreenSectionLimit caps per-section item limits.
	MaxScreenSectionLimit = 100
	// MaxScreenLeagues bounds static league filter chips declared on one section.
	MaxScreenLeagues = 64
)

var screenSlugRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

var validScreenSectionTypes = map[string]bool{
	"hero":     true,
	"events":   true,
	"table":    true,
	"schedule": true,
	"leagues":  true,
	"entities": true,
}

var validScreenEventStates = map[string]bool{
	"pre":       true,
	"in":        true,
	"post":      true,
	"delayed":   true,
	"postponed": true,
	"canceled":  true,
}

var validScreenEventWindows = map[string]bool{
	"today":  true,
	"week":   true,
	"recent": true,
}

var validScreenFrontends = map[string]bool{
	"vela": true,
}

// ScreenMeta declares one generic top-level screen contributed by a plugin
// (spec A3). Published screens appear in frontend navigation and resolve their
// sections generically on demand.
type ScreenMeta struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Icon      string          `json:"icon,omitempty"`
	Feed      string          `json:"feed,omitempty"`
	Sections  []ScreenSection `json:"sections"`
	Frontends []string        `json:"frontends,omitempty"`
}

// ScreenSection declares one generic section inside a contributed screen.
type ScreenSection struct {
	ID         string            `json:"id,omitempty"`
	Type       string            `json:"type"` // hero|events|table|schedule|leagues|entities
	Title      string            `json:"title,omitempty"`
	Feed       string            `json:"feed,omitempty"`
	League     string            `json:"league,omitempty"`
	EntityKind string            `json:"entityKind,omitempty"`
	Limit      int               `json:"limit,omitempty"`
	Query      *ScreenEventQuery `json:"query,omitempty"`
	Leagues    []ScreenLeague    `json:"leagues,omitempty"`
}

// ScreenEventQuery filters live events for an events, hero, or schedule section.
type ScreenEventQuery struct {
	Feed      string   `json:"feed,omitempty"`
	State     []string `json:"state,omitempty"` // pre|in|post|delayed|postponed|canceled
	League    string   `json:"league,omitempty"`
	Favorites bool     `json:"favorites,omitempty"`
	Window    string   `json:"window,omitempty"` // today|week|recent
	Category  string   `json:"category,omitempty"`
}

// ScreenLeague optionally declares one static league filter chip on a leagues section.
type ScreenLeague struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Abbr string `json:"abbr,omitempty"`
	Logo string `json:"logo,omitempty"`
}

func validateScreens(m Manifest, add func(string, string), label func(string, string, int)) {
	screens := m.Ether.Contributes.Screens
	if len(screens) == 0 {
		return
	}
	if len(screens) > MaxPluginScreens {
		add("ether.contributes.screens", fmt.Sprintf("must declare at most %d screens", MaxPluginScreens))
	}
	seenScreens := map[string]bool{}
	for i, sc := range screens {
		base := fmt.Sprintf("ether.contributes.screens[%d]", i)
		if !screenSlugRe.MatchString(sc.ID) || seenScreens[sc.ID] {
			add(base+".id", "must be a unique slug matching ^[a-z][a-z0-9-]{0,31}$")
		}
		seenScreens[sc.ID] = true
		label(base+".title", sc.Title, 40)
		if sc.Icon != "" && !screenSlugRe.MatchString(sc.Icon) {
			add(base+".icon", "must be a slug matching ^[a-z][a-z0-9-]{0,31}$")
		}
		if sc.Feed != "" && !keyRe.MatchString(sc.Feed) {
			add(base+".feed", "must be a simple key")
		}
		seenFrontends := map[string]bool{}
		for j, fe := range sc.Frontends {
			if !validScreenFrontends[fe] || seenFrontends[fe] {
				add(fmt.Sprintf("%s.frontends[%d]", base, j), "must be a unique supported frontend such as vela")
			}
			seenFrontends[fe] = true
		}
		if len(sc.Sections) == 0 {
			add(base+".sections", "must declare at least one section")
			continue
		}
		if len(sc.Sections) > MaxScreenSections {
			add(base+".sections", fmt.Sprintf("must declare at most %d sections", MaxScreenSections))
		}
		seenSections := map[string]bool{}
		for j, sec := range sc.Sections {
			sbase := fmt.Sprintf("%s.sections[%d]", base, j)
			if sec.ID != "" {
				if !screenSlugRe.MatchString(sec.ID) || seenSections[sec.ID] {
					add(sbase+".id", "must be a unique slug matching ^[a-z][a-z0-9-]{0,31}$")
				}
				seenSections[sec.ID] = true
			}
			if !validScreenSectionTypes[sec.Type] {
				add(sbase+".type", "must be hero, events, table, schedule, leagues or entities")
			}
			if sec.Title != "" {
				label(sbase+".title", sec.Title, 60)
			}
			if sec.Feed != "" && !keyRe.MatchString(sec.Feed) {
				add(sbase+".feed", "must be a simple key")
			}
			if sec.EntityKind != "" && !keyRe.MatchString(sec.EntityKind) {
				add(sbase+".entityKind", "must be a simple key")
			}
			if sec.Limit < 0 || sec.Limit > MaxScreenSectionLimit {
				add(sbase+".limit", fmt.Sprintf("must be between 0 and %d", MaxScreenSectionLimit))
			}
			if sec.Query != nil {
				if sec.Type == "table" || sec.Type == "leagues" || sec.Type == "entities" {
					add(sbase+".query", "is only allowed on hero, events or schedule sections")
				} else {
					validateScreenEventQuery(sbase+".query", sec.Query, add)
				}
			}
			if len(sec.Leagues) > 0 {
				if sec.Type != "leagues" {
					add(sbase+".leagues", "is only allowed on leagues sections")
				}
				if len(sec.Leagues) > MaxScreenLeagues {
					add(sbase+".leagues", fmt.Sprintf("must declare at most %d leagues", MaxScreenLeagues))
				}
				seenLeagues := map[string]bool{}
				for k, lg := range sec.Leagues {
					lbase := fmt.Sprintf("%s.leagues[%d]", sbase, k)
					id := strings.TrimSpace(lg.ID)
					if id == "" || seenLeagues[id] {
						add(lbase+".id", "must be a unique non-empty id")
					}
					seenLeagues[id] = true
					if strings.TrimSpace(lg.Name) == "" {
						add(lbase+".name", "is required")
					}
				}
			}
		}
	}
}

func validateScreenEventQuery(base string, q *ScreenEventQuery, add func(string, string)) {
	if q.Feed != "" && !keyRe.MatchString(q.Feed) {
		add(base+".feed", "must be a simple key")
	}
	seenStates := map[string]bool{}
	for i, st := range q.State {
		if !validScreenEventStates[st] {
			add(fmt.Sprintf("%s.state[%d]", base, i), "unknown event state "+strconv.Quote(st))
		} else if seenStates[st] {
			add(fmt.Sprintf("%s.state[%d]", base, i), "duplicate event state "+strconv.Quote(st))
		}
		seenStates[st] = true
	}
	if q.Window != "" && !validScreenEventWindows[q.Window] {
		add(base+".window", "must be today, week or recent")
	}
}
