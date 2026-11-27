package personalize

import (
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
)

// filterCounts tallies the candidates a generator's viewer filters removed:
// already watched or in progress, a language the viewer rarely watches,
// too little-known for the row. It is counted while the row is built, so
// explaining an empty row never needs another query.
type filterCounts struct {
	history, language, notable int
}

func (c filterCounts) add(o filterCounts) filterCounts {
	return filterCounts{c.history + o.history, c.language + o.language, c.notable + o.notable}
}

// rowFilterStats is filterCounts per base type, so the Movies and TV Shows
// pages explain their own half of a mixed row.
type rowFilterStats struct {
	movie, show filterCounts
}

func (s *rowFilterStats) of(t core.MediaType) *filterCounts {
	if s == nil {
		return nil
	}
	switch baseType(t) {
	case core.TypeMovie:
		return &s.movie
	case core.TypeShow:
		return &s.show
	}
	return nil
}

func (s *rowFilterStats) forSection(section core.MediaType) filterCounts {
	switch section {
	case core.TypeMovie:
		return s.movie
	case core.TypeShow:
		return s.show
	}
	return s.movie.add(s.show)
}

// watchesLanguage is the viewer's language gate, tallied for the row being
// built.
func (b *builder) watchesLanguage(t *core.Title) bool {
	if b.taste.watchesLanguage(t.Language) {
		return true
	}
	if c := b.tally.of(t.Type); c != nil {
		c.language++
	}
	return false
}

// watchedOrStarted reports a title the viewer has history with: the
// part of fresh() that is about this viewer rather than the title.
func (b *builder) watchedOrStarted(t *core.Title) bool {
	if b.touched[tasteKey(t.Key)] {
		return true
	}
	ws := b.statuses[t.Key]
	return ws.ViewCount != 0 || ws.ViewOffset != 0
}

// filteredRowReason explains why a generated row has too few titles to
// show for this viewer, from the counts its filters kept while it was
// built. ok is false when the row had enough titles (it was absent for
// another reason) or nothing was filtered.
func filteredRowReason(c *candRow, section core.MediaType, pinned bool) (code, reason string, ok bool) {
	if c == nil || c.filtered == nil {
		return "", "", false
	}
	n := 0
	for _, it := range c.items {
		if section == "" || baseType(it.t.Type) == section {
			n++
		}
	}
	if section != "" && c.typed != nil {
		if typed, has := c.typed[section]; has {
			n = len(typed)
		}
	}
	min := max(c.minItems, 1)
	if pinned {
		min = 1
	}
	if n >= min {
		return "", "", false
	}
	f := c.filtered.forSection(section)
	cause, most := "", 0
	for _, k := range []struct {
		name string
		n    int
	}{{"history", f.history}, {"language", f.language}, {"notable", f.notable}} {
		if k.n > most {
			cause, most = k.name, k.n
		}
	}
	if n > 0 {
		why := ""
		switch cause {
		case "history":
			why = "; the rest were already watched or in progress"
		case "language":
			why = "; the rest are in languages this viewer rarely watches"
		case "notable":
			why = "; the rest are too little-known for this row"
		}
		return "below_minimum", fmt.Sprintf("Fewer than %d titles left for this viewer (%d)%s", min, n, why), true
	}
	switch cause {
	case "history":
		return "filtered_by_history", "Already watched or in progress for this viewer", true
	case "language":
		return "filtered_by_language", "Only titles in languages this viewer rarely watches", true
	case "notable":
		return "filtered_by_popularity", "Only little-known titles left for this viewer", true
	}
	return "", "", false
}
