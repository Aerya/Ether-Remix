package liveevents

import (
	"math"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/braedonsaunders/ether/internal/core"
)

const (
	// WatchWindowBefore and WatchWindowAfter define the A5 guide overlap window
	// around an event's scheduled start time (start - 30m to start + 4h).
	WatchWindowBefore = 30 * 60 // seconds (30m)
	WatchWindowAfter  = 4 * 3600

	// eventChannelWindowBefore bounds how far before kickoff a dynamic IPTV
	// per-game event channel (whose current name carries no date) may match a
	// scheduled event, preventing multi-day series or next-week collisions.
	eventChannelWindowBefore = 12 * time.Hour

	minGuideMatchConfidence = 0.45
	maxWatchTargetsPerEvent = 16
	maxAlternatesPerTarget  = 8
)

// GuideCandidate is the package-neutral view of one viewer-visible Live TV
// channel + guide programme (or a guide-free IPTV channel) used by the A5/A6
// matcher.
type GuideCandidate struct {
	ChannelID     string
	ChannelName   string
	ChannelNumber string
	ChannelLogo   string
	TvgName       string
	GuideID       string
	Group         string
	IsEventAddon  bool
	ChannelOnly   bool
	Favorite      bool
	WatchedAt     time.Time
	Unhealthy     bool
	Title         string
	EpisodeTitle  string
	Summary       string
	Genres        []string
	Start         time.Time
	End           time.Time
	Live          bool
}

type watchMatchTier int

const (
	watchTierGuide       watchMatchTier = 0 // EPG guide match (or Stremio event addon programme)
	watchTierEventChan   watchMatchTier = 1 // Dynamic IPTV per-event / league event channel
	watchTierBroadcaster watchMatchTier = 2 // Guide-free broadcaster-name fallback
	watchTierPlugin      watchMatchTier = 3 // Direct playable plugin stream URL
)

type competitorTerms struct {
	phrases [][]string // normalized multi-token or single-token terms for this competitor
}

type eventMatchProfile struct {
	state             string            // event lifecycle state (pre|in|post|delayed|...)
	eventChanWindow   bool              // true when now is within the event-channel match window
	singleEvent       bool              // true for non-head-to-head events (F1, PGA, tournaments, etc.)
	hasCardTitle      bool              // true for numbered/titled card events (e.g. "UFC 310: ...")
	leagueFamily      string            // canonical league family (e.g. "nfl", "nba", "epl", "ufc", "f1")
	editionLeague     string            // numbered edition league prefix (e.g. "ufc")
	editionNumber     string            // numbered edition identifier (e.g. "310")
	competitors       []competitorTerms // per-competitor phrases (2 for head-to-head)
	extraTerms        [][]string        // matchHints.terms + event title phrases
	eventTitlePhrases [][]string        // distinctive event title phrases (incl. GP <-> Grand Prix, colon-prefix)
	leaguePhrases     [][]string        // distinctive league phrases for single-event sports
	callsigns         [][]string        // normalized broadcast names + matchHints.callsigns
	linearCallsigns   [][]string        // non-OTT linear broadcast callsigns for guide-free fallback
	category          []string          // normalized category/league words for genre fallback
}

// foldText normalizes Unicode superscripts/compatibility badges (e.g. ᴴᴰ, ᶠᴴᴰ,
// ᵁᴴᴰ, ⁴ᴷ, ˢᴰ), small-caps, fullwidth punctuation, and diacritics into plain
// lowercase-ready ASCII/Unicode text, separating attached superscript badges
// like "Eventᴴᴰ" into "Event HD".
func foldText(s string) string {
	if s == "" {
		return ""
	}
	var pre strings.Builder
	pre.Grow(len(s) + 8)
	prevNormal := false
	prevSup := false
	for _, r := range s {
		switch r {
		case '™', '®', '©':
			pre.WriteByte(' ')
			prevNormal = false
			prevSup = false
			continue
		}
		mapped, isSup := mapSuperscriptOrSmallCap(r)
		if isSup {
			if prevNormal {
				pre.WriteByte(' ')
			}
			pre.WriteRune(mapped)
			prevSup = true
			prevNormal = false
			continue
		}
		isNormal := unicode.IsLetter(r) || unicode.IsDigit(r)
		if isNormal && prevSup {
			pre.WriteByte(' ')
		}
		pre.WriteRune(r)
		prevNormal = isNormal
		prevSup = false
	}

	decomposed := norm.NFKD.String(pre.String())
	var out strings.Builder
	out.Grow(len(decomposed))
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func mapSuperscriptOrSmallCap(r rune) (rune, bool) {
	switch r {
	// Common IPTV small-caps not decomposed by NFKD.
	case 'ꜰ':
		return 'f', true
	case 'ʜ':
		return 'h', true
	case 'ᴅ':
		return 'd', true
	case 'ᴜ':
		return 'u', true
	case 'ᴋ':
		return 'k', true
	case 'ꜱ':
		return 's', true
	// Superscript digits (NFKD decomposes them, but separating them here keeps
	// "ESPN⁴ᴷ" split from "ESPN").
	case '⁰':
		return '0', true
	case '¹':
		return '1', true
	case '²':
		return '2', true
	case '³':
		return '3', true
	case '⁴':
		return '4', true
	case '⁵':
		return '5', true
	case '⁶':
		return '6', true
	case '⁷':
		return '7', true
	case '⁸':
		return '8', true
	case '⁹':
		return '9', true
	}
	if unicode.Is(unicode.Lm, r) {
		// Modifier letters such as ᴴ ᴰ ᶠ ᵁ ᴷ ᴿ ᴬ ᵂ ˢ decompose under NFKD.
		return r, true
	}
	return r, false
}

// normalizeTokensRaw folds Unicode compatibility/diacritic forms, lowercases,
// splits on non-alphanumeric runs, preserves '+' as "plus", and joins
// apostrophes inside tokens without dropping connector words.
func normalizeTokensRaw(s string) []string {
	if s == "" {
		return nil
	}
	folded := foldText(s)
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		out = append(out, cur.String())
		cur.Reset()
	}
	for _, r := range folded {
		r = unicode.ToLower(r)
		switch {
		case r == '+':
			flush()
			out = append(out, "plus")
		case r == '\'' || r == '\u2019':
			// Join apostrophes inside names ("A's", "St. John's") without breaking token.
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cur.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// normalizeTokens lowercases text, folds superscripts/diacritics, splits on
// non-alphanumeric Unicode runs, preserves '+' as "plus", and strips connector
// words ("vs", "v", "versus", "at"). Punctuation such as "@" is stripped as a
// separator automatically.
func normalizeTokens(s string) []string {
	raw := normalizeTokensRaw(s)
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, tok := range raw {
		switch tok {
		case "vs", "v", "versus", "at":
			continue
		}
		out = append(out, tok)
	}
	return out
}

func isRegionTag(tok string) bool {
	switch tok {
	case "us", "usa", "uk", "gb", "ca", "can", "au", "aus", "ie", "ire",
		"nz", "eu", "de", "fr", "es", "it", "nl", "mx", "br", "en", "intl", "int":
		return true
	default:
		return false
	}
}

func isBackupOrAltTag(tok string) bool {
	switch tok {
	case "backup", "bkup", "bkp", "bak", "alt", "alternate", "feed":
		return true
	default:
		return false
	}
}

func isQualityOrFeedTag(tok string) bool {
	if isBackupOrAltTag(tok) {
		return true
	}
	switch tok {
	case "hd", "fhd", "uhd", "sd", "4k", "8k", "hq", "lq", "hevc", "h264", "h265",
		"raw", "1080p", "1080i", "720p", "2160p", "540p", "480p",
		"50fps", "60fps", "east", "west", "vip", "low", "high", "english", "eng":
		return true
	default:
		return false
	}
}

func isNetworkSuffix(tok string) bool {
	switch tok {
	case "tv", "network", "channel", "chnl", "ch":
		return true
	default:
		return false
	}
}

// normalizeCallsignTokens strips IPTV country/region prefixes & suffixes,
// separators, quality/feed tags (HD/FHD/UHD/4K/SD/ᴴᴰ/backup/alt/1080p/50fps),
// East/West, and generic network suffixes while preserving meaningful channel
// numbers ("ESPN 2", "TSN 1", "Sky Sports F1", "FOX Sports 1") and brand names
// ("USA Network", "ABC Australia").
func normalizeCallsignTokens(s string) []string {
	raw := normalizeTokens(s)
	if len(raw) == 0 {
		return nil
	}

	// isSuffixOnly reports whether every token in slice is a strip-only quality,
	// feed, or network suffix (not a region tag or brand token).
	isSuffixOnly := func(slice []string) bool {
		for i := 0; i < len(slice); i++ {
			tok := slice[i]
			if isQualityOrFeedTag(tok) || isNetworkSuffix(tok) || tok == "net" || tok == "fps" {
				continue
			}
			if (tok == "50" || tok == "60") && i+1 < len(slice) && slice[i+1] == "fps" {
				i++
				continue
			}
			if isAllDigits(tok) && i > 0 && isBackupOrAltTag(slice[i-1]) {
				continue
			}
			return false
		}
		return true
	}

	// hasBrandTokens reports whether slice contains an actual network brand after
	// stripping a leading prefix — including the "USA" / "USA Network" brand.
	hasBrandTokens := func(slice []string) bool {
		for i := 0; i < len(slice); i++ {
			tok := slice[i]
			if tok == "usa" && isSuffixOnly(slice[i+1:]) {
				return true
			}
			if isRegionTag(tok) || isQualityOrFeedTag(tok) || isNetworkSuffix(tok) || tok == "net" || tok == "fps" {
				continue
			}
			if (tok == "50" || tok == "60") && i+1 < len(slice) && slice[i+1] == "fps" {
				i++
				continue
			}
			if isAllDigits(tok) && i > 0 && isBackupOrAltTag(slice[i-1]) {
				continue
			}
			return true
		}
		return false
	}

	// Strip leading country/region and quality/VIP prefixes while preserving
	// "USA" / "USA Network" / "USA HD" as the USA Network brand.
	start := 0
	for start < len(raw)-1 {
		tok := raw[start]
		if tok == "usa" && isSuffixOnly(raw[start+1:]) {
			break
		}
		if (isRegionTag(tok) || isQualityOrFeedTag(tok)) && hasBrandTokens(raw[start+1:]) {
			start++
			continue
		}
		break
	}

	out := make([]string, 0, len(raw)-start)
	for i := start; i < len(raw); i++ {
		tok := raw[i]
		if (tok == "50" || tok == "60") && i+1 < len(raw) && raw[i+1] == "fps" {
			i++
			continue
		}
		if tok == "fps" {
			continue
		}
		if isBackupOrAltTag(tok) {
			// Skip trailing backup/alt slot numbers (e.g. "ESPN Backup 2" -> "espn")
			// without stripping channel numbers that precede backup ("ESPN 2 Backup" -> "espn 2").
			if i+1 < len(raw) && isAllDigits(raw[i+1]) && !hasBrandTokens(raw[i+2:]) {
				i++
			}
			continue
		}
		if isQualityOrFeedTag(tok) || isNetworkSuffix(tok) {
			continue
		}
		if tok == "net" && len(out) > 0 {
			continue
		}
		out = append(out, tok)
	}

	// Strip trailing region codes (e.g. "beIN SPORTS 1 USA" -> ["bein", "sports", "1"])
	// when preceded by actual network tokens. Full country names like "Australia"
	// are not region codes and remain intact ("ABC Australia").
	for len(out) >= 2 && isRegionTag(out[len(out)-1]) {
		out = out[:len(out)-1]
	}

	if len(out) == 0 {
		return raw
	}
	return out
}

// channelFeedQuality scores feed quality from raw channel metadata so duplicate
// feeds of the same network prefer 4K/UHD > FHD/1080p > HD > plain > SD > backup/alt.
func channelFeedQuality(name, tvgName string) int {
	scoreTokens := func(toks []string) (score int, hasQuality bool, isBackup bool) {
		best := -1
		bonus := 0
		for i, tok := range toks {
			if isBackupOrAltTag(tok) || tok == "lq" || tok == "low" {
				isBackup = true
				continue
			}
			switch tok {
			case "8k":
				if 50 > best {
					best = 50
				}
			case "4k", "uhd", "2160p":
				if 40 > best {
					best = 40
				}
			case "fhd", "1080p", "1080i", "raw":
				if 30 > best {
					best = 30
				}
			case "hd", "720p", "hq", "high":
				if 20 > best {
					best = 20
				}
			case "sd", "540p", "480p":
				if 5 > best {
					best = 5
				}
			case "60fps", "50fps":
				bonus += 3
			case "fps":
				if i > 0 && (toks[i-1] == "50" || toks[i-1] == "60") {
					bonus += 3
				}
			case "hevc", "h265":
				bonus++
			}
		}
		if best >= 0 {
			hasQuality = true
			score = best + bonus
		} else {
			score = 10 + bonus
		}
		if isBackup {
			score -= 100
		}
		return score, hasQuality, isBackup
	}

	s1, hasQ, isB := scoreTokens(normalizeTokensRaw(name))
	if hasQ || isB || tvgName == "" {
		return s1
	}
	s2, hasQ2, isB2 := scoreTokens(normalizeTokensRaw(tvgName))
	if hasQ2 || isB2 {
		return s2
	}
	return s1
}

// eventChannelWindowOK reports whether dynamic IPTV event channels (which carry
// only the current game's title and no date) may match ev at time now.
func eventChannelWindowOK(ev core.LiveEvent, now time.Time) bool {
	switch ev.Status.State {
	case core.LiveEventStatePost, core.LiveEventStateCanceled, core.LiveEventStatePostponed:
		return false
	case core.LiveEventStateIn, core.LiveEventStateDelayed:
		return true
	}
	start := parseEventTime(ev.Start)
	if start.IsZero() {
		return false
	}
	if now.IsZero() {
		return true
	}
	after := time.Duration(WatchWindowAfter) * time.Second
	return !now.Before(start.Add(-eventChannelWindowBefore)) && !now.After(start.Add(after))
}

func buildEventMatchProfile(ev core.LiveEvent, now time.Time) eventMatchProfile {
	prof := eventMatchProfile{
		state:           ev.Status.State,
		eventChanWindow: eventChannelWindowOK(ev, now),
		singleEvent:     len(ev.Competitors) != 2,
	}

	addPhrase := func(dst *[][]string, raw string) {
		toks := normalizeTokens(raw)
		if len(toks) == 0 {
			return
		}
		if len(toks) == 1 && len(toks[0]) < 2 {
			return
		}
		for _, existing := range *dst {
			if slices.Equal(existing, toks) {
				return
			}
		}
		*dst = append(*dst, toks)
	}
	addTokens := func(dst *[][]string, toks []string) {
		if len(toks) == 0 {
			return
		}
		if len(toks) == 1 && len(toks[0]) < 2 {
			return
		}
		for _, existing := range *dst {
			if slices.Equal(existing, toks) {
				return
			}
		}
		*dst = append(*dst, toks)
	}

	for _, c := range ev.Competitors {
		var ct competitorTerms
		addPhrase(&ct.phrases, c.Name)
		addPhrase(&ct.phrases, c.Short)
		addPhrase(&ct.phrases, c.Abbr)
		// Also include the last distinctive word of multi-word team names
		// (e.g., "City Eagles" -> "eagles") when >= 3 characters so guides
		// and event channels listing nicknames match reliably.
		if nameToks := normalizeTokens(c.Name); len(nameToks) >= 2 {
			last := nameToks[len(nameToks)-1]
			if len(last) >= 3 && !isGenericTeamSuffix(last) {
				addPhrase(&ct.phrases, last)
			}
		}
		if len(ct.phrases) > 0 {
			prof.competitors = append(prof.competitors, ct)
		}
	}

	if ev.MatchHints != nil {
		for _, term := range ev.MatchHints.Terms {
			toks := normalizeTokens(term)
			if len(toks) == 0 {
				continue
			}
			// Associate hint terms that overlap a competitor with that competitor
			// too, so plugin-supplied abbreviations/nicknames count toward matching
			// both competitors.
			associated := false
			for i := range prof.competitors {
				if sharesTokenWithPhrases(toks, prof.competitors[i].phrases) {
					addPhrase(&prof.competitors[i].phrases, term)
					associated = true
				}
			}
			if !associated && len(toks) == 1 && len(toks[0]) >= 3 && len(prof.competitors) > 0 {
				matchIdx := -1
				for i := range prof.competitors {
					if hasTokenPrefixInPhrases(toks[0], prof.competitors[i].phrases) {
						if matchIdx >= 0 {
							matchIdx = -2
							break
						}
						matchIdx = i
					}
				}
				if matchIdx >= 0 {
					addPhrase(&prof.competitors[matchIdx].phrases, term)
				}
			}
			addPhrase(&prof.extraTerms, term)
		}
	}

	if ev.Title != "" {
		addPhrase(&prof.extraTerms, ev.Title)
	}

	// Derive event title phrases (colon prefix like "UFC 310" from
	// "UFC 310: Pantoja vs. Asakura", sponsor-stripped Grand Prix / GP variants
	// like "Las Vegas GP" from "Heineken Las Vegas Grand Prix").
	for _, rawTitle := range collectEventTitleSources(ev) {
		for _, p := range deriveEventTitlePhrases(rawTitle) {
			addTokens(&prof.eventTitlePhrases, p)
			addTokens(&prof.extraTerms, p)
		}
	}

	// Detect numbered card editions (e.g. "UFC 310", "Bellator 300") and card-mode events.
	for _, p := range prof.eventTitlePhrases {
		if len(p) == 2 && !isAllDigits(p[0]) && isAllDigits(p[1]) && len(p[1]) >= 2 {
			prof.editionLeague = p[0]
			prof.editionNumber = p[1]
			prof.hasCardTitle = true
			break
		}
	}
	if !prof.hasCardTitle && ev.Title != "" && isCardCategory(ev.Category, ev.League) {
		prof.hasCardTitle = true
	}

	appendUniqueTokens := func(dst *[][]string, toks []string) {
		if len(toks) == 0 {
			return
		}
		for _, existing := range *dst {
			if slices.Equal(existing, toks) {
				return
			}
		}
		*dst = append(*dst, toks)
	}
	addCallsign := func(raw string, allowLinear bool) {
		toks := normalizeCallsignTokens(raw)
		if len(toks) == 0 {
			return
		}
		variants := expandCallsignAliases(toks)
		for _, v := range variants {
			appendUniqueTokens(&prof.callsigns, v)
			if allowLinear && !isStreamingOnlyCallsign(v) {
				appendUniqueTokens(&prof.linearCallsigns, v)
			}
		}
	}
	for _, b := range ev.Broadcasts {
		if b.Kind == "radio" {
			continue
		}
		addCallsign(b.Name, b.Kind != "stream")
	}
	if ev.MatchHints != nil {
		for _, cs := range ev.MatchHints.Callsigns {
			addCallsign(cs, true)
		}
	}

	for _, src := range []string{ev.Category, ev.League.Name, ev.League.Abbr} {
		for _, tok := range normalizeTokens(src) {
			if len(tok) >= 2 && !slices.Contains(prof.category, tok) {
				prof.category = append(prof.category, tok)
			}
		}
	}
	for _, src := range []string{ev.League.Name, ev.League.Abbr, ev.League.ID} {
		toks := normalizeTokens(src)
		if len(toks) == 0 {
			continue
		}
		if len(toks) == 1 && len(toks[0]) < 2 {
			continue
		}
		addTokens(&prof.leaguePhrases, toks)
		if fam := canonicalLeagueFamily(toks); fam != "" && prof.leagueFamily == "" {
			prof.leagueFamily = fam
		}
	}
	if prof.leagueFamily == "" {
		prof.leagueFamily = canonicalLeagueFamily(prof.category)
	}
	// Expand common league phrase synonyms (e.g., F1 <-> Formula 1, EPL <-> Premier League).
	switch prof.leagueFamily {
	case "f1":
		addTokens(&prof.leaguePhrases, []string{"f1"})
		addTokens(&prof.leaguePhrases, []string{"formula", "1"})
	case "epl":
		addTokens(&prof.leaguePhrases, []string{"epl"})
		addTokens(&prof.leaguePhrases, []string{"premier", "league"})
	case "ufc":
		addTokens(&prof.leaguePhrases, []string{"ufc"})
	case "pga":
		addTokens(&prof.leaguePhrases, []string{"pga"})
		addTokens(&prof.leaguePhrases, []string{"pga", "tour"})
	case "ucl":
		addTokens(&prof.leaguePhrases, []string{"ucl"})
		addTokens(&prof.leaguePhrases, []string{"champions", "league"})
	}

	return prof
}

func isGenericTeamSuffix(tok string) bool {
	switch tok {
	case "fc", "sc", "cf", "ac", "bc", "city", "united", "town", "club", "athletic", "sporting":
		return true
	default:
		return false
	}
}

func isCardCategory(cat string, lg core.LiveEventLeague) bool {
	switch strings.ToLower(strings.TrimSpace(cat)) {
	case "mma", "fighting", "boxing", "wrestling":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(lg.ID)) {
	case "ufc", "bellator", "pfl", "boxing", "wwe":
		return true
	}
	return false
}

func collectEventTitleSources(ev core.LiveEvent) []string {
	var out []string
	if strings.TrimSpace(ev.Title) != "" {
		out = append(out, ev.Title)
	}
	if ev.MatchHints != nil && len(ev.Competitors) != 2 {
		out = append(out, ev.MatchHints.Terms...)
	}
	return out
}

// deriveEventTitlePhrases extracts clean event-title phrases including colon
// prefixes ("UFC 310" from "UFC 310: Pantoja vs. Asakura") and Grand Prix / GP
// variants ("Las Vegas GP" and "Las Vegas Grand Prix" from sponsor-prefixed
// titles such as "Heineken Las Vegas Grand Prix").
func deriveEventTitlePhrases(raw string) [][]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out [][]string
	add := func(toks []string) {
		if len(toks) == 0 {
			return
		}
		if len(toks) == 1 && len(toks[0]) < 3 && !isShortDistinctiveAlnum(toks[0]) {
			return
		}
		for _, e := range out {
			if slices.Equal(e, toks) {
				return
			}
		}
		out = append(out, toks)
	}

	// Header separated by ':' or " - " (e.g. "UFC 310: Pantoja vs. Asakura" ->
	// "UFC 310", "UFC 310 - Main Card" -> "UFC 310", "Formula 1: Las Vegas GP").
	for _, sep := range []string{":", " - "} {
		if idx := strings.Index(raw, sep); idx > 0 {
			left := normalizeTokens(raw[:idx])
			if len(left) >= 2 {
				add(left)
			}
			right := normalizeTokens(raw[idx+len(sep):])
			if len(right) >= 2 {
				add(right)
			}
		}
	}

	toks := normalizeTokens(raw)
	if len(toks) >= 2 {
		add(toks)
		// Leading numbered card edition (e.g. "UFC 310 ..." -> ["ufc", "310"]).
		if !isAllDigits(toks[0]) && isAllDigits(toks[1]) && len(toks[1]) >= 2 {
			add([]string{toks[0], toks[1]})
		}
	}

	// Grand Prix <-> GP normalization and sponsor-prefix stripping (including
	// titles with trailing years such as "Las Vegas Grand Prix 2026").
	addGPVariants := func(t []string) {
		for i := 0; i < len(t); i++ {
			var prefix []string
			if i >= 1 && i+1 < len(t) && t[i] == "grand" && t[i+1] == "prix" {
				prefix = t[:i]
			} else if i >= 1 && t[i] == "gp" {
				prefix = t[:i]
			} else {
				continue
			}
			add(append(slices.Clone(prefix), "grand", "prix"))
			add(append(slices.Clone(prefix), "gp"))
			// Strip leading sponsor word(s) so "Heineken Las Vegas Grand Prix"
			// yields "Las Vegas Grand Prix" and "Las Vegas GP".
			for drop := 1; drop < len(prefix); drop++ {
				if len(prefix)-drop >= 1 && len(prefix)-drop <= 3 {
					sub := prefix[drop:]
					add(append(slices.Clone(sub), "grand", "prix"))
					add(append(slices.Clone(sub), "gp"))
				}
			}
		}
	}
	addGPVariants(toks)
	return out
}

func canonicalLeagueFamily(tokens []string) string {
	for i, tok := range tokens {
		switch tok {
		case "nfl":
			return "nfl"
		case "nba":
			return "nba"
		case "wnba":
			return "wnba"
		case "mlb":
			return "mlb"
		case "nhl":
			return "nhl"
		case "cfl":
			return "cfl"
		case "epl":
			return "epl"
		case "mls":
			return "mls"
		case "ufc", "mma":
			return "ufc"
		case "f1":
			return "f1"
		case "formula":
			if i+1 < len(tokens) && tokens[i+1] == "1" {
				return "f1"
			}
		case "pga":
			return "pga"
		case "ucl":
			return "ucl"
		case "champions":
			if i+1 < len(tokens) && tokens[i+1] == "league" {
				return "ucl"
			}
		case "premier":
			if i+1 < len(tokens) && tokens[i+1] == "league" {
				return "epl"
			}
		case "ncaa", "ncaaf", "ncaab":
			return "ncaa"
		case "laliga":
			return "laliga"
		case "la":
			if i+1 < len(tokens) && tokens[i+1] == "liga" {
				return "laliga"
			}
		case "serie":
			if i+1 < len(tokens) && tokens[i+1] == "a" {
				return "seriea"
			}
		case "bundesliga":
			return "bundesliga"
		case "ligue":
			if i+1 < len(tokens) && tokens[i+1] == "1" {
				return "ligue1"
			}
		case "nascar":
			return "nascar"
		case "motogp":
			return "motogp"
		case "indycar":
			return "indycar"
		}
	}
	return ""
}

func collectLeagueFamilies(tokens []string) []string {
	var fams []string
	add := func(f string) {
		if f != "" && !slices.Contains(fams, f) {
			fams = append(fams, f)
		}
	}
	for i := range tokens {
		add(canonicalLeagueFamily(tokens[i:min(i+2, len(tokens))]))
	}
	return fams
}

func hasEventGroupCue(groupToks []string) bool {
	if len(groupToks) == 0 {
		return false
	}
	for _, tok := range groupToks {
		switch tok {
		case "events", "event", "ppv", "pass", "ticket", "matchday", "matches",
			"match", "innings", "ice", "live", "fight", "fights", "direct", "box":
			return true
		}
	}
	return false
}

func hasEventNameCue(nameToks []string) bool {
	for _, tok := range nameToks {
		switch tok {
		case "ppv", "events", "event", "live", "pass", "pro", "tv", "feed", "direct", "match", "game":
			return true
		}
	}
	return false
}

func hasMatchupSeparator(raw string, rawToks []string) bool {
	if strings.ContainsRune(raw, '@') {
		return true
	}
	for _, tok := range rawToks {
		switch tok {
		case "vs", "v", "versus", "at":
			return true
		}
	}
	return false
}

func sharesTokenWithPhrases(toks []string, phrases [][]string) bool {
	for _, p := range phrases {
		for _, pt := range p {
			if len(pt) < 2 {
				continue
			}
			for _, t := range toks {
				if t == pt {
					return true
				}
			}
		}
	}
	return false
}

func hasTokenPrefixInPhrases(prefix string, phrases [][]string) bool {
	for _, p := range phrases {
		for _, pt := range p {
			if len(pt) > len(prefix) && strings.HasPrefix(pt, prefix) {
				return true
			}
		}
	}
	return false
}

// containsPhrase reports whether needle occurs as a contiguous token sequence
// inside haystack.
func containsPhrase(haystack, needle []string) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		if slices.Equal(haystack[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

// phraseMatched reports whether any phrase in phrases occurs in haystack, and
// whether a distinctive phrase (multi-word or >= 3 chars) matched.
func phraseMatched(haystack []string, phrases [][]string) (matched bool, distinctive bool) {
	for _, p := range phrases {
		if !containsPhrase(haystack, p) {
			continue
		}
		matched = true
		if len(p) >= 2 || (len(p) == 1 && len(p[0]) >= 3) {
			distinctive = true
			return true, true
		}
	}
	return matched, distinctive
}

// callsignMatchesChannel checks whether any normalized event callsign matches
// the channel's name, tvgName, or guideId without confusing numbered channels
// or unrelated sister networks (e.g. FOX vs Fox News / FS1, TNT vs TNT Sports 1,
// ESPN vs ESPN2).
func callsignMatchesChannel(callsigns [][]string, cand GuideCandidate) (matched bool, exact bool) {
	if len(callsigns) == 0 {
		return false, false
	}
	fields := [][]string{
		normalizeCallsignTokens(cand.ChannelName),
		normalizeCallsignTokens(cand.TvgName),
		normalizeCallsignTokens(cand.GuideID),
	}
	for _, cs := range callsigns {
		for _, field := range fields {
			ok, isExact := callsignMatchesField(cs, field)
			if isExact {
				return true, true
			}
			if ok {
				matched = true
			}
		}
	}
	return matched, false
}

func callsignMatchesField(callsign, field []string) (matched bool, exact bool) {
	if len(callsign) == 0 || len(field) == 0 || len(callsign) > len(field) {
		return false, false
	}
	if slices.Equal(callsign, field) {
		return true, true
	}
	callsignHasSports := slices.Contains(callsign, "sports") || slices.Contains(callsign, "sport")
	for i := 0; i <= len(field)-len(callsign); i++ {
		if !slices.Equal(field[i:i+len(callsign)], callsign) {
			continue
		}
		// Single-token callsigns (e.g. "fox", "usa", "golf") must anchor at the
		// start of the channel name so they do not falsely match mid-name brands
		// like "Silver Fox Movies" or "beIN Sports USA".
		if i > 0 && len(callsign) == 1 {
			continue
		}
		// Prevent matching distinct sister networks, event-package channels, or
		// numbered sub-channels when the callsign did not name them (e.g. FOX vs
		// Fox News / FS1 / FOX Sports 1, TNT vs TNT Sports 1, ESPN vs ESPN2 / ESPN+,
		// NFL Network vs NFL 03 / NFL RedZone, ABC vs ABC Australia).
		nextIdx := i + len(callsign)
		if nextIdx < len(field) {
			next := field[nextIdx]
			if isAllDigits(next) {
				continue
			}
			switch next {
			case "plus", "news", "business", "movies", "movie", "cinema",
				"kids", "junior", "jr", "music", "radio", "deportes", "espanol",
				"australia", "canada", "ireland", "extra", "xtra",
				"redzone", "ppv", "pass", "ticket", "sunday", "league", "game", "games",
				"event", "events", "match", "matches", "live", "box", "office",
				"f1", "golf", "cricket", "tennis", "racing", "football", "soccer",
				"baseball", "basketball", "hockey", "fighting", "mma", "boxing", "ufc",
				"premier", "main", "arena", "action", "mix", "select", "now", "go":
				if !slices.Contains(callsign, next) {
					continue
				}
			case "sports", "sport":
				if !callsignHasSports {
					continue
				}
			}
		}
		return true, false
	}
	return false, false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func hasSportsGenre(genres []string, categoryTokens []string) bool {
	for _, g := range genres {
		toks := normalizeTokens(g)
		for _, tok := range toks {
			switch tok {
			case "sport", "sports", "athletics", "racing", "motorsport", "motorsports", "esports", "esport":
				return true
			}
			if slices.Contains(categoryTokens, tok) {
				return true
			}
		}
	}
	return false
}

func isGenericLiveSportsTitle(tokens []string) bool {
	return slices.Contains(tokens, "live") && (slices.Contains(tokens, "sports") || slices.Contains(tokens, "sport"))
}

// scoreEventChannel evaluates whether an IPTV channel's current name (and group)
// identifies it as a dynamic per-game or single-event channel for prof within
// the event window (e.g. "NFL 03: Giants vs Eagles 8:15 PM ET",
// "US| NBA 05 | Celtics @ Knicks", "EPL 2: Liverpool v Arsenal (16:30)",
// "PPV 4 - UFC 310", "F1 TV: Las Vegas GP").
func scoreEventChannel(prof eventMatchProfile, cand GuideCandidate) (float64, bool) {
	if cand.IsEventAddon || !prof.eventChanWindow {
		return 0, false
	}
	nameToks := normalizeTokens(cand.ChannelName)
	tvgToks := normalizeTokens(cand.TvgName)
	if len(nameToks) == 0 && len(tvgToks) == 0 {
		return 0, false
	}
	groupToks := normalizeTokens(cand.Group)
	rawNameToks := normalizeTokensRaw(cand.ChannelName)
	hasSep := hasMatchupSeparator(cand.ChannelName, rawNameToks)

	// Guard against cross-league homonyms (e.g. MLB "Giants" vs NFL "Giants",
	// NHL "Jets" vs NFL "Jets"): if the channel name or group explicitly labels
	// a different league family and does not include this event's league, reject.
	chanFams := collectLeagueFamilies(nameToks)
	groupFams := collectLeagueFamilies(groupToks)
	allFams := make([]string, 0, len(chanFams)+len(groupFams))
	allFams = append(allFams, chanFams...)
	for _, gf := range groupFams {
		if !slices.Contains(allFams, gf) {
			allFams = append(allFams, gf)
		}
	}
	leagueMatch := false
	if prof.leagueFamily != "" && len(allFams) > 0 {
		if slices.Contains(allFams, prof.leagueFamily) {
			leagueMatch = true
		} else {
			return 0, false
		}
	}
	if !leagueMatch && len(prof.leaguePhrases) > 0 {
		if ok, _ := phraseMatched(nameToks, prof.leaguePhrases); ok {
			leagueMatch = true
		} else if ok, _ := phraseMatched(groupToks, prof.leaguePhrases); ok {
			leagueMatch = true
		}
	}
	eventGroupCue := hasEventGroupCue(groupToks)

	// Guard against numbered edition conflicts (e.g. "PPV 5 - UFC 311" vs "UFC 310").
	if prof.editionLeague != "" && prof.editionNumber != "" {
		if hasConflictingEdition(nameToks, prof.editionLeague, prof.editionNumber) ||
			hasConflictingEdition(tvgToks, prof.editionLeague, prof.editionNumber) {
			return 0, false
		}
	}

	// 1. Head-to-head competitor match in channel name (or tvgName).
	if len(prof.competitors) == 2 {
		matchCompetitors := func(field []string) (float64, bool) {
			if len(field) == 0 {
				return 0, false
			}
			c0, c0Dist := phraseMatched(field, prof.competitors[0].phrases)
			c1, c1Dist := phraseMatched(field, prof.competitors[1].phrases)
			if !c0 || !c1 {
				return 0, false
			}
			// Short 2-letter abbreviations require explicit matchup or league/event context.
			if (!c0Dist || !c1Dist) && !hasSep && !leagueMatch && !eventGroupCue {
				return 0, false
			}
			score := 0.78
			if c0Dist && c1Dist {
				score = 0.82
			}
			if leagueMatch {
				score += 0.02
			}
			if eventGroupCue {
				score += 0.02
			}
			if hasSep && score < 0.86 {
				score += 0.01
			}
			if score > 0.86 {
				score = 0.86
			}
			return clampConfidence(score), true
		}
		if score, ok := matchCompetitors(nameToks); ok {
			return score, true
		}
		if score, ok := matchCompetitors(tvgToks); ok {
			return score, true
		}
	}

	// 2. Single-event or numbered card sports (F1, PGA, UFC, boxing, tournaments).
	if prof.singleEvent || prof.hasCardTitle {
		matchEventTitle := func(field []string) (float64, bool) {
			if len(field) == 0 {
				return 0, false
			}
			hits := countDistinctPhraseHits(field, prof.eventTitlePhrases)
			if hits == 0 && !hasStrongTitleOverlap(field, prof.eventTitlePhrases) {
				return 0, false
			}
			score := 0.82
			if leagueMatch || eventGroupCue || hasEventNameCue(field) {
				score = 0.84
			}
			return clampConfidence(score), true
		}
		if score, ok := matchEventTitle(nameToks); ok {
			return score, true
		}
		if score, ok := matchEventTitle(tvgToks); ok {
			return score, true
		}

		// Dedicated league event channel (e.g. "F1 TV", "US| UFC PPV 1") when
		// the channel is NOT already a regular linear broadcast network and does
		// not name a different specific event (e.g. "F1 TV: Monaco GP").
		if matchedLinear, _ := callsignMatchesChannel(prof.linearCallsigns, cand); matchedLinear {
			return 0, false
		}
		if channelNamesOtherSpecificEvent(cand.ChannelName, rawNameToks, nameToks) {
			return 0, false
		}
		leagueInName, _ := phraseMatched(nameToks, prof.leaguePhrases)
		if !leagueInName && len(tvgToks) > 0 {
			leagueInName, _ = phraseMatched(tvgToks, prof.leaguePhrases)
		}
		if leagueInName && (eventGroupCue || hasEventNameCue(nameToks) || hasEventNameCue(tvgToks)) {
			score := 0.76
			if eventGroupCue {
				score = 0.78
			}
			return clampConfidence(score), true
		}
	}

	return 0, false
}

func hasConflictingEdition(tokens []string, league, wantNum string) bool {
	for i := 0; i < len(tokens)-1; i++ {
		if tokens[i] == league && isAllDigits(tokens[i+1]) && len(tokens[i+1]) >= 2 && tokens[i+1] != wantNum {
			return true
		}
	}
	return false
}

// channelNamesOtherSpecificEvent reports whether a channel name appears to label
// a specific event (e.g. "F1 TV: Monaco GP", "PGA: Pebble Beach Open", "A vs B")
// so the league-only fallback never attaches an unrelated stale event channel.
func channelNamesOtherSpecificEvent(rawName string, rawToks, normToks []string) bool {
	if hasMatchupSeparator(rawName, rawToks) {
		return true
	}
	for i, tok := range normToks {
		switch tok {
		case "gp", "open", "masters", "championship", "cup", "bowl", "classic", "invitational", "derby":
			return true
		case "grand":
			if i+1 < len(normToks) && normToks[i+1] == "prix" {
				return true
			}
		}
	}
	return false
}

// scoreGuideCandidate evaluates a single Live TV or Stremio event channel
// candidate against an event profile. Returns (confidence in [0, 1], tier, ok).
func scoreGuideCandidate(prof eventMatchProfile, cand GuideCandidate) (float64, watchMatchTier, bool) {
	// Guide-free IPTV channel (no overlapping EPG programme): check dynamic
	// event channel first (higher confidence & tier), then broadcaster callsign.
	if cand.ChannelOnly {
		if cand.IsEventAddon {
			return 0, 0, false
		}
		if conf, ok := scoreEventChannel(prof, cand); ok {
			return conf, watchTierEventChan, true
		}
		linearMatch, linearExact := callsignMatchesChannel(prof.linearCallsigns, cand)
		if !linearMatch {
			return 0, 0, false
		}
		switch prof.state {
		case core.LiveEventStatePost, core.LiveEventStateCanceled, core.LiveEventStatePostponed:
			return 0, 0, false
		}
		if linearExact {
			return 0.60, watchTierBroadcaster, true
		}
		return 0.52, watchTierBroadcaster, true
	}

	if conf, ok := scoreGuideProgramme(prof, cand); ok {
		return conf, watchTierGuide, true
	}

	// If an event channel carries generic/placeholder EPG (e.g. "No Information")
	// that did not match the event, still allow dynamic event-channel matching on
	// its channel name + group within the event window.
	if conf, ok := scoreEventChannel(prof, cand); ok {
		return conf, watchTierEventChan, true
	}
	return 0, 0, false
}

func scoreGuideProgramme(prof eventMatchProfile, cand GuideCandidate) (float64, bool) {
	callsignMatch, _ := callsignMatchesChannel(prof.callsigns, cand)

	titleTokens := normalizeTokens(cand.Title)
	if cand.EpisodeTitle != "" {
		titleTokens = append(titleTokens, normalizeTokens(cand.EpisodeTitle)...)
	}
	summaryTokens := normalizeTokens(cand.Summary)
	allTokens := make([]string, 0, len(titleTokens)+len(summaryTokens))
	allTokens = append(allTokens, titleTokens...)
	allTokens = append(allTokens, summaryTokens...)

	sportsGenre := hasSportsGenre(cand.Genres, prof.category)

	// Head-to-head competitor matching (2 competitors).
	if len(prof.competitors) == 2 {
		c0Title, c0TitleDist := phraseMatched(titleTokens, prof.competitors[0].phrases)
		c1Title, c1TitleDist := phraseMatched(titleTokens, prof.competitors[1].phrases)
		c0Any, c0AnyDist := phraseMatched(allTokens, prof.competitors[0].phrases)
		c1Any, c1AnyDist := phraseMatched(allTokens, prof.competitors[1].phrases)

		bothTitle := c0Title && c1Title
		bothAny := c0Any && c1Any
		oneTitle := (c0Title && c0TitleDist) || (c1Title && c1TitleDist)
		oneAny := (c0Any && c0AnyDist) || (c1Any && c1AnyDist)

		cardTitleMatch := prof.hasCardTitle && (countDistinctPhraseHits(titleTokens, prof.eventTitlePhrases) > 0 || hasStrongTitleOverlap(titleTokens, prof.eventTitlePhrases))
		if prof.editionLeague != "" && prof.editionNumber != "" && hasConflictingEdition(titleTokens, prof.editionLeague, prof.editionNumber) {
			cardTitleMatch = false
		}

		// 0.3.18 Stremio event channels (A5 source #2): require both competitors or card title.
		if cand.IsEventAddon {
			if !bothAny && !cardTitleMatch {
				return 0, false
			}
			score := 0.78
			if bothTitle || cardTitleMatch {
				score = 0.88
			}
			if c0TitleDist && c1TitleDist {
				score += 0.04
			}
			return clampConfidence(score), true
		}

		// Standard Live TV guide programme (A5 source #1).
		var score float64
		switch {
		case bothTitle:
			score = 0.68
			if c0TitleDist || c1TitleDist {
				score += 0.04
			}
		case cardTitleMatch:
			score = 0.68
		case bothAny:
			score = 0.56
		case oneTitle && (callsignMatch || sportsGenre):
			score = 0.32
		case oneAny && callsignMatch && sportsGenre:
			score = 0.24
		}

		catInTitle := matchesCategoryInTokens(titleTokens, prof.category)
		liveSportsTitle := isGenericLiveSportsTitle(titleTokens)
		if callsignMatch {
			if score > 0 {
				score += 0.24
			} else if sportsGenre || catInTitle || liveSportsTitle {
				// Broadcast channel matches AND programme is live sports / matches
				// league/category during the event window even if EPG omitted teams.
				score = 0.36
			}
		}

		if sportsGenre && score > 0 {
			score += 0.12
		} else if (catInTitle || liveSportsTitle) && score > 0 {
			score += 0.10
		}

		if score < minGuideMatchConfidence {
			return 0, false
		}
		return clampConfidence(score), true
	}

	// N-competitor or non-head-to-head event (e.g., motorsport, golf, single-title event).
	extraTitleHits := countDistinctPhraseHits(titleTokens, prof.extraTerms)
	extraAnyHits := countDistinctPhraseHits(allTokens, prof.extraTerms)
	if hasStrongTitleOverlap(titleTokens, prof.extraTerms) {
		extraTitleHits++
		extraAnyHits++
	}
	catInTitle := matchesCategoryInTokens(titleTokens, prof.category)
	liveSportsTitle := isGenericLiveSportsTitle(titleTokens)

	if cand.IsEventAddon {
		if extraTitleHits == 0 && extraAnyHits < 2 {
			return 0, false
		}
		score := 0.75
		if extraTitleHits >= 1 {
			score = 0.85
		}
		return clampConfidence(score), true
	}

	var score float64
	switch {
	case extraTitleHits >= 2:
		score = 0.68
	case extraTitleHits == 1:
		score = 0.50
	case extraAnyHits >= 2:
		score = 0.48
	}

	if callsignMatch {
		if score > 0 {
			score += 0.24
		} else if sportsGenre || catInTitle || liveSportsTitle {
			score = 0.36
		}
	}
	if sportsGenre && score > 0 {
		score += 0.12
	} else if (catInTitle || liveSportsTitle) && score > 0 {
		score += 0.10
	}

	if score < minGuideMatchConfidence {
		return 0, false
	}
	return clampConfidence(score), true
}

func isShortDistinctiveAlnum(tok string) bool {
	if len(tok) != 2 {
		return false
	}
	hasLetter := false
	hasDigit := false
	for _, r := range tok {
		if unicode.IsLetter(r) {
			hasLetter = true
		} else if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	return hasLetter && hasDigit
}

func countDistinctPhraseHits(tokens []string, phrases [][]string) int {
	hits := 0
	for _, p := range phrases {
		if len(p) == 1 && len(p[0]) < 3 && !isShortDistinctiveAlnum(p[0]) {
			continue
		}
		if containsPhrase(tokens, p) {
			hits++
		}
	}
	return hits
}

// hasStrongTitleOverlap reports whether tokens cover most distinctive words of
// any multi-word event title phrase (handles title-sponsor prefixes like
// "Heineken Las Vegas Grand Prix" against "Formula 1: Las Vegas Grand Prix").
func hasStrongTitleOverlap(tokens []string, phrases [][]string) bool {
	if len(tokens) == 0 {
		return false
	}
	for _, p := range phrases {
		if len(p) < 3 {
			continue
		}
		distinctive := 0
		matched := 0
		for _, word := range p {
			if len(word) < 3 {
				continue
			}
			distinctive++
			if slices.Contains(tokens, word) {
				matched++
			}
		}
		if distinctive >= 3 && matched >= 3 && matched*4 >= distinctive*3 {
			return true
		}
	}
	return false
}

func matchesCategoryInTokens(tokens, category []string) bool {
	if len(tokens) == 0 || len(category) == 0 {
		return false
	}
	for _, c := range category {
		if len(c) >= 2 && slices.Contains(tokens, c) {
			return true
		}
	}
	return false
}

func clampConfidence(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return math.Round(v*100) / 100
}

// isPlayableStreamURL reports whether rawURL is a directly playable stream
// reference (plugin:// or streaming media scheme/manifest) rather than an
// informational web page such as an ESPN Gamecast/Commentary/Odds URL.
func isPlayableStreamURL(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "plugin", "rtmp", "rtmps", "rtsp", "rtsps", "udp", "rtp":
		return true
	case "http", "https":
		p := strings.ToLower(u.Path)
		return strings.HasSuffix(p, ".m3u8") ||
			strings.HasSuffix(p, ".mpd") ||
			strings.HasSuffix(p, ".ts") ||
			strings.HasSuffix(p, ".mp4")
	default:
		return false
	}
}

type scoredWatchTarget struct {
	target      core.WatchTarget
	tier        watchMatchTier
	favorite    bool
	watchedAt   time.Time
	feedQuality int
	dedupeKey   string
}

func boolPtr(v bool) *bool { return &v }

func canonicalNetworkKey(cand GuideCandidate, tier watchMatchTier) string {
	if cand.IsEventAddon {
		return "addon:" + cand.ChannelID
	}
	toks := normalizeCallsignTokens(cand.ChannelName)
	if len(toks) == 0 {
		toks = normalizeCallsignTokens(cand.TvgName)
	}
	if len(toks) == 0 {
		return "ch:" + cand.ChannelID
	}
	if tier == watchTierEventChan {
		return "event:" + strings.Join(toks, " ")
	}
	key := canonicalizeCallsignKey(toks)
	return "net:" + key
}

// canonicalizeCallsignKey maps equivalent broadcaster aliases onto a single
// canonical dedupe key (e.g. FS1 <-> FOX Sports 1, ESPN2 <-> ESPN 2, TSN1 <-> TSN 1)
// while keeping distinct networks separate (ESPN != ESPN 2, FOX != FOX Sports 1).
func canonicalizeCallsignKey(toks []string) string {
	key := strings.Join(toks, " ")
	switch key {
	case "fs1":
		return "fox sports 1"
	case "fs2":
		return "fox sports 2"
	case "espn2":
		return "espn 2"
	case "cbssn":
		return "cbs sports"
	case "nbatv":
		return "nba"
	case "trutv":
		return "tru"
	case "nfln":
		return "nfl"
	case "mlbn":
		return "mlb"
	case "nhln":
		return "nhl"
	case "sn1", "sportsnet one":
		return "sportsnet 1"
	case "sn360":
		return "sportsnet 360"
	case "sky sports pl":
		return "sky sports premier league"
	case "sky sports me":
		return "sky sports main event"
	}
	if len(toks) == 1 {
		if alpha, num, ok := splitAlphaTrailingDigits(toks[0]); ok && len(alpha) >= 3 {
			return alpha + " " + num
		}
	}
	return key
}

func betterScoredTarget(a, b scoredWatchTarget) bool {
	if avA, avB := a.target.IsAvailable(), b.target.IsAvailable(); avA != avB {
		return avA
	}
	if a.tier != b.tier {
		return a.tier < b.tier
	}
	if a.favorite != b.favorite {
		return a.favorite
	}
	aRecent := !a.watchedAt.IsZero()
	bRecent := !b.watchedAt.IsZero()
	if aRecent != bRecent {
		return aRecent
	}
	if aRecent && !a.watchedAt.Equal(b.watchedAt) {
		return a.watchedAt.After(b.watchedAt)
	}
	if a.target.Confidence != b.target.Confidence {
		return a.target.Confidence > b.target.Confidence
	}
	if a.target.Live != b.target.Live {
		return a.target.Live
	}
	if a.feedQuality != b.feedQuality {
		return a.feedQuality > b.feedQuality
	}
	if ra, rb := watchKindRank(a.target.Kind), watchKindRank(b.target.Kind); ra != rb {
		return ra < rb
	}
	if a.target.Name != b.target.Name {
		return a.target.Name < b.target.Name
	}
	return a.target.ChannelID < b.target.ChannelID
}

// matchEventToCandidates scores guide candidates + playable plugin broadcast
// streams for ev using ev.Start as the reference clock (convenience wrapper for
// unit tests).
// Unhealthy channels are excluded whenever any healthy target exists; when only
// unhealthy channels match, they are returned with available=false so clients
// can render "Unavailable right now" instead of a Watch button.
// Informational reference links (detail.Links) are excluded per spec A5.
func matchEventToCandidates(ev core.LiveEvent, candidates []GuideCandidate) []core.WatchTarget {
	return matchEventToCandidatesAt(ev, candidates, parseEventTime(ev.Start))
}

// matchEventToCandidatesAt scores guide candidates + playable plugin broadcast
// streams for ev at time now. Healthy targets always come before unhealthy
// ones; when any healthy target exists, unhealthy targets are excluded, and
// when only unhealthy channels match, they are returned with available=false
// (including on collapsed alternates). Within the selected set, targets are
// ordered guide -> event channel -> broadcaster -> plugin, preferring
// viewer-favourited and recently watched channels, and collapsing duplicate
// feeds of the same canonical network to the best one with alternates retained.
func matchEventToCandidatesAt(ev core.LiveEvent, candidates []GuideCandidate, now time.Time) []core.WatchTarget {
	prof := buildEventMatchProfile(ev, now)
	eventLive := ev.Status.State == core.LiveEventStateIn

	// Deduplicate by channelId, retaining the highest-ranked match per channel
	// (preferring healthy over unhealthy if both appear for the same channel).
	bestByChannel := make(map[string]scoredWatchTarget)
	for _, cand := range candidates {
		conf, tier, ok := scoreGuideCandidate(prof, cand)
		if !ok {
			continue
		}
		kind := core.WatchTargetKindChannel
		if cand.IsEventAddon {
			kind = core.WatchTargetKindAddon
		}
		live := cand.Live
		if cand.ChannelOnly || tier == watchTierEventChan {
			live = eventLive
		}
		st := scoredWatchTarget{
			target: core.WatchTarget{
				Kind:       kind,
				ChannelID:  cand.ChannelID,
				Name:       cand.ChannelName,
				Number:     cand.ChannelNumber,
				Logo:       cand.ChannelLogo,
				Live:       live,
				Confidence: conf,
			},
			tier:        tier,
			favorite:    cand.Favorite,
			watchedAt:   cand.WatchedAt,
			feedQuality: channelFeedQuality(cand.ChannelName, cand.TvgName),
			dedupeKey:   canonicalNetworkKey(cand, tier),
		}
		if cand.Unhealthy {
			st.target.Available = boolPtr(false)
		}
		prev, exists := bestByChannel[cand.ChannelID]
		if !exists || betterScoredTarget(st, prev) {
			bestByChannel[cand.ChannelID] = st
		}
	}

	healthy := make([]scoredWatchTarget, 0, len(bestByChannel)+len(ev.Broadcasts))
	var unhealthy []scoredWatchTarget
	for _, st := range bestByChannel {
		if st.target.IsAvailable() {
			healthy = append(healthy, st)
		} else {
			unhealthy = append(unhealthy, st)
		}
	}

	// Source #3: Plugin-provided directly playable stream URLs (never web pages).
	seenURL := make(map[string]struct{})
	for _, b := range ev.Broadcasts {
		u := strings.TrimSpace(b.URL)
		if !isPlayableStreamURL(u) {
			continue
		}
		if _, dup := seenURL[u]; dup {
			continue
		}
		seenURL[u] = struct{}{}
		name := strings.TrimSpace(b.Name)
		if name == "" {
			name = "Stream"
		}
		healthy = append(healthy, scoredWatchTarget{
			target: core.WatchTarget{
				Kind:       core.WatchTargetKindPlugin,
				Name:       name,
				URL:        u,
				Live:       eventLive,
				Confidence: 0.55,
			},
			tier:      watchTierPlugin,
			dedupeKey: "plugin:" + u,
		})
	}

	active := healthy
	if len(active) == 0 {
		active = unhealthy
	}
	sort.Slice(active, func(i, j int) bool {
		return betterScoredTarget(active[i], active[j])
	})

	// Collapse duplicate feeds of the same canonical network (HD/FHD/4K/backup/alt)
	// to the best feed, attaching the rest as Alternates in ranked order while
	// preserving each alternate's Available flag.
	out := make([]core.WatchTarget, 0, len(active))
	indexByKey := make(map[string]int, len(active))
	for _, st := range active {
		if idx, exists := indexByKey[st.dedupeKey]; exists {
			if len(out[idx].Alternates) < maxAlternatesPerTarget {
				out[idx].Alternates = append(out[idx].Alternates, st.target)
			}
			continue
		}
		indexByKey[st.dedupeKey] = len(out)
		out = append(out, st.target)
	}

	if len(out) > maxWatchTargetsPerEvent {
		out = out[:maxWatchTargetsPerEvent]
	}
	return out
}

func watchKindRank(kind string) int {
	switch kind {
	case core.WatchTargetKindChannel:
		return 0
	case core.WatchTargetKindAddon:
		return 1
	case core.WatchTargetKindPlugin:
		return 2
	default:
		return 3
	}
}

func sortWatchTargets(targets []core.WatchTarget) {
	sort.Slice(targets, func(i, j int) bool {
		a, b := targets[i], targets[j]
		if avA, avB := a.IsAvailable(), b.IsAvailable(); avA != avB {
			return avA
		}
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		if a.Live != b.Live {
			return a.Live
		}
		if ra, rb := watchKindRank(a.Kind), watchKindRank(b.Kind); ra != rb {
			return ra < rb
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ChannelID < b.ChannelID
	})
}

// expandCallsignAliases returns normalized token variants for common broadcast
// abbreviations (e.g. FS1 <-> FOX Sports 1, ESPN2 <-> ESPN 2, TSN 1 <-> TSN1,
// beIN Sports -> beIN Sports 1) without blurring distinct sibling channels
// (FOX vs FS1, ESPN vs ESPN2, TNT vs TNT Sports 1).
func expandCallsignAliases(toks []string) [][]string {
	out := [][]string{toks}
	add := func(v []string) {
		for _, e := range out {
			if slices.Equal(e, v) {
				return
			}
		}
		out = append(out, v)
	}
	key := strings.Join(toks, " ")
	switch key {
	case "fs1":
		add([]string{"fox", "sports", "1"})
	case "fox sports 1":
		add([]string{"fs1"})
	case "fs2":
		add([]string{"fox", "sports", "2"})
	case "fox sports 2":
		add([]string{"fs2"})
	case "espn2":
		add([]string{"espn", "2"})
	case "espn 2":
		add([]string{"espn2"})
	case "nbatv":
		add([]string{"nba"})
	case "cbssn":
		add([]string{"cbs", "sports"})
	case "cbs sports":
		add([]string{"cbssn"})
	case "tru":
		add([]string{"trutv"})
	case "trutv":
		add([]string{"tru"})
	case "nfln":
		add([]string{"nfl"})
	case "mlbn":
		add([]string{"mlb"})
	case "nhln":
		add([]string{"nhl"})
	case "bein sports":
		add([]string{"bein", "sports", "1"})
	case "bein sports 1":
		add([]string{"bein", "sports"})
	case "sn1", "sportsnet 1", "sportsnet one":
		add([]string{"sn1"})
		add([]string{"sportsnet", "1"})
		add([]string{"sportsnet", "one"})
	case "sn360":
		add([]string{"sportsnet", "360"})
	case "sky sports pl":
		add([]string{"sky", "sports", "premier", "league"})
	case "sky sports me":
		add([]string{"sky", "sports", "main", "event"})
	}
	// General "<word> <digit>" <-> "<word><digit>" (e.g. TSN 1 <-> TSN1, DAZN 1 <-> DAZN1).
	if len(toks) == 2 && len(toks[0]) >= 3 && isAllDigits(toks[1]) && !containsDigit(toks[0]) {
		add([]string{toks[0] + toks[1]})
	} else if len(toks) == 1 {
		if alpha, num, ok := splitAlphaTrailingDigits(toks[0]); ok && len(alpha) >= 3 {
			add([]string{alpha, num})
		}
	}
	return out
}

func containsDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func splitAlphaTrailingDigits(s string) (string, string, bool) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	if i == 0 || i == len(s) {
		return "", "", false
	}
	for j := 0; j < i; j++ {
		if s[j] < 'a' || s[j] > 'z' {
			return "", "", false
		}
	}
	return s[:i], s[i:], true
}

// isStreamingOnlyCallsign identifies OTT streaming services (like Peacock or
// ESPN+) that are not linear TV channels and must not produce guide-free
// ChannelOnly watch or channel-link matches.
func isStreamingOnlyCallsign(toks []string) bool {
	switch strings.Join(toks, " ") {
	case "peacock", "espn plus", "paramount plus", "apple", "apple plus",
		"prime video", "amazon prime", "amazon", "netflix", "hulu", "max",
		"fubo", "fandango", "fandango home", "youtube", "nba league pass",
		"mls season pass", "nfl plus", "discovery plus", "stan sport",
		"viaplay", "vict plus":
		return true
	default:
		return false
	}
}

// matchProgrammeToEvents performs the reverse of A5 (for A6 Live TV overlay):
// given a currently airing channel programme and active/near-active events,
// pick the highest-confidence matching LiveEventRef, or nil if none meets the
// threshold.
func matchProgrammeToEvents(cand GuideCandidate, events []core.LiveEvent, now time.Time) *core.LiveEventRef {
	var bestRef *core.LiveEventRef
	bestTier := watchTierPlugin + 1
	bestScore := 0.0
	bestLive := false
	for _, ev := range events {
		if ev.ID == "" {
			continue
		}
		if ev.Status.State == core.LiveEventStateCanceled || ev.Status.State == core.LiveEventStatePostponed {
			continue
		}
		prof := buildEventMatchProfile(ev, now)
		conf, tier, ok := scoreGuideCandidate(prof, cand)
		if !ok {
			continue
		}
		isLive := ev.Status.State == core.LiveEventStateIn
		better := bestRef == nil ||
			tier < bestTier ||
			(tier == bestTier && conf > bestScore) ||
			(tier == bestTier && conf == bestScore && isLive && !bestLive) ||
			(tier == bestTier && conf == bestScore && isLive == bestLive && ev.ID < bestRef.ID)
		if better {
			ref := core.LiveEventRef{ID: ev.ID, Feed: ev.Feed}
			bestRef = &ref
			bestTier = tier
			bestScore = conf
			bestLive = isLive
		}
	}
	return bestRef
}

// matchChannelOnlyToEvents performs the A6 guide-free fallback when a live
// channel has no current EPG programme: first match dynamic event channels
// unambiguously by channel name/group, then fall back to linking the single
// live event whose linear broadcasts match that channel (breaking ties with a
// single viewer-favourited entity).
func matchChannelOnlyToEvents(cand GuideCandidate, events []core.LiveEvent, favKeys []string, now time.Time) *core.LiveEventRef {
	if cand.IsEventAddon {
		return nil
	}
	// First check dynamic event-channel matching (e.g. "NFL 03: Giants vs Eagles").
	var bestEventRef *core.LiveEventRef
	bestEventScore := 0.0
	for _, ev := range events {
		if ev.ID == "" || ev.Status.State != core.LiveEventStateIn {
			continue
		}
		prof := buildEventMatchProfile(ev, now)
		conf, ok := scoreEventChannel(prof, cand)
		if !ok {
			continue
		}
		if bestEventRef == nil || conf > bestEventScore || (conf == bestEventScore && ev.ID < bestEventRef.ID) {
			ref := core.LiveEventRef{ID: ev.ID, Feed: ev.Feed}
			bestEventRef = &ref
			bestEventScore = conf
		}
	}
	if bestEventRef != nil {
		return bestEventRef
	}

	var exactMatches []core.LiveEvent
	var prefixMatches []core.LiveEvent
	for _, ev := range events {
		if ev.ID == "" || ev.Status.State != core.LiveEventStateIn {
			continue
		}
		prof := buildEventMatchProfile(ev, now)
		matched, exact := callsignMatchesChannel(prof.linearCallsigns, cand)
		if !matched {
			continue
		}
		if exact {
			exactMatches = append(exactMatches, ev)
		} else {
			prefixMatches = append(prefixMatches, ev)
		}
	}
	matches := exactMatches
	if len(matches) == 0 {
		matches = prefixMatches
	}
	if len(matches) == 0 {
		return nil
	}
	if len(matches) == 1 {
		return &core.LiveEventRef{ID: matches[0].ID, Feed: matches[0].Feed}
	}
	// Multiple live events match the same network: only link when exactly one
	// features a viewer-favourited competitor or ranking entity.
	if len(favKeys) == 0 {
		return nil
	}
	var favMatches []core.LiveEvent
	for _, ev := range matches {
		if eventMatchesAnyFavoriteKey(ev, favKeys) {
			favMatches = append(favMatches, ev)
		}
	}
	if len(favMatches) == 1 {
		return &core.LiveEventRef{ID: favMatches[0].ID, Feed: favMatches[0].Feed}
	}
	return nil
}

func eventMatchesAnyFavoriteKey(ev core.LiveEvent, favKeys []string) bool {
	pluginID, feedID, _, ok := core.ParseLiveEventID(ev.ID)
	if !ok {
		return false
	}
	for _, fk := range favKeys {
		fp, ff, _, fEntityID, fok := core.ParseLiveEntityKey(fk)
		if !fok || fp != pluginID || ff != feedID {
			continue
		}
		for _, c := range ev.Competitors {
			if c.EntityID != "" && c.EntityID == fEntityID {
				return true
			}
		}
		for _, r := range ev.Ranking {
			if r.EntityID != "" && r.EntityID == fEntityID {
				return true
			}
		}
	}
	return false
}
