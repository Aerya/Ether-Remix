package livetv

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	autoBackupWindow     = 6 * time.Hour
	autoBackupGuideBatch = 150
	autoBackupWriteBatch = 150
)

var extraDuplicateRegions = map[string]bool{
	"usa": true, "can": true, "gbr": true, "eng": true, "sco": true, "wal": true, "nir": true,
	"aus": true, "nzl": true, "irl": true, "deu": true, "ger": true, "fra": true, "esp": true,
	"ita": true, "nld": true, "bel": true, "che": true, "aut": true, "prt": true, "pol": true,
	"swe": true, "nor": true, "dnk": true, "fin": true, "mex": true, "bra": true, "arg": true,
	"ind": true, "zaf": true, "uae": true,
}

var genericProgrammeTitles = map[string]bool{
	"":                       true,
	"tba":                    true,
	"tbd":                    true,
	"tobeannounced":          true,
	"tobedetermined":         true,
	"noinformation":          true,
	"noinformationavailable": true,
	"noinfo":                 true,
	"nodata":                 true,
	"nolistings":             true,
	"offair":                 true,
	"signoff":                true,
	"unknown":                true,
	"programmenotavailable":  true,
	"programnotavailable":    true,
}

func hasAutoStreamSources(c Channel) bool {
	for i := range c.StreamSources {
		if c.StreamSources[i].AutoFrom != "" {
			return true
		}
	}
	return false
}

func isDuplicateRegionCode(lower string) bool {
	if lower == "" {
		return false
	}
	if _, ok := guideRegionCodes[lower]; ok {
		return true
	}
	return extraDuplicateRegions[lower]
}

func isUpperDuplicateRegion(tok string) bool {
	lower, ok := upperGuideToken(tok)
	return ok && isDuplicateRegionCode(lower)
}

// classifyQualityToken returns one of qualityTier* and whether tok is a
// resolution/quality/framerate marker. bareNumeric reports plain numbers like
// "1080" or "720" so a channel named solely "1080" is not folded to empty.
func classifyQualityToken(lower string) (tier int, isMarker bool, bareNumeric bool) {
	switch lower {
	case "4k", "uhd", "4kuhd", "uhd4k", "8k":
		return qualityTier4K, true, false
	case "fhd", "fullhd", "qhd":
		return qualityTierFHD, true, false
	case "hd":
		return qualityTierHD, true, false
	case "sd":
		return qualityTierSD, true, false
	case "hq", "lq", "raw", "hevc", "h264", "h265", "avc", "hdr", "hdr10", "sdr":
		return qualityTierUnknown, true, false
	}
	if strings.HasSuffix(lower, "fps") {
		num := strings.TrimSuffix(lower, "fps")
		if isDigitsLen(num, 2, 3) {
			return qualityTierUnknown, true, false
		}
	}
	for _, p := range []struct {
		prefix string
		tier   int
	}{
		{"4k", qualityTier4K},
		{"8k", qualityTier4K},
		{"uhd", qualityTier4K},
		{"fhd", qualityTierFHD},
		{"hd", qualityTierHD},
	} {
		if strings.HasPrefix(lower, p.prefix) {
			rest := strings.TrimSuffix(lower[len(p.prefix):], "fps")
			if isDigitsLen(rest, 2, 3) {
				return p.tier, true, false
			}
		}
	}
	for _, r := range []struct {
		lines string
		tier  int
	}{
		{"4320", qualityTier4K},
		{"2160", qualityTier4K},
		{"1440", qualityTierFHD},
		{"1080", qualityTierFHD},
		{"720", qualityTierHD},
		{"576", qualityTierSD},
		{"540", qualityTierSD},
		{"480", qualityTierSD},
	} {
		if !strings.HasPrefix(lower, r.lines) {
			continue
		}
		rest := lower[len(r.lines):]
		if rest == "" {
			return r.tier, true, true
		}
		if rest[0] == 'p' || rest[0] == 'i' {
			fps := strings.TrimSuffix(rest[1:], "fps")
			if fps == "" || isDigitsLen(fps, 2, 3) {
				return r.tier, true, false
			}
		}
	}
	return qualityTierUnknown, false, false
}

func isDigitsLen(s string, minLen, maxLen int) bool {
	if len(s) < minLen || len(s) > maxLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func splitNameTokens(s string) []string {
	raw := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '+'
	})
	out := raw[:0]
	for _, tok := range raw {
		if strings.Trim(tok, "+") == "" {
			continue
		}
		out = append(out, tok)
	}
	return out
}

func stripBracketedDuplicateTags(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		open := s[i]
		var closeCh byte
		switch open {
		case '(':
			closeCh = ')'
		case '[':
			closeCh = ']'
		case '{':
			closeCh = '}'
		}
		if closeCh == 0 {
			b.WriteByte(open)
			i++
			continue
		}
		end := -1
		for j := i + 1; j < len(s); j++ {
			if s[j] == '(' || s[j] == '[' || s[j] == '{' {
				break
			}
			if s[j] == closeCh {
				end = j
				break
			}
		}
		if end < 0 {
			b.WriteByte(open)
			i++
			continue
		}
		inner := s[i+1 : end]
		b.WriteByte(' ')
		for _, tok := range splitNameTokens(inner) {
			lower := strings.ToLower(tok)
			_, isMarker, _ := classifyQualityToken(lower)
			if isMarker || isDuplicateRegionCode(lower) {
				continue
			}
			b.WriteString(tok)
			b.WriteByte(' ')
		}
		i = end + 1
	}
	return b.String()
}

// foldDuplicateChannelName normalizes an IPTV channel display name for
// duplicate-channel detection: bracketed/pipe/colon region and quality markers
// ("HD", "FHD", "4K", "720p", "1080p", "1080p60", "US", "CA", "UK", etc.) are
// stripped while "+" suffixes ("+1", "ESPN+") remain distinct.
func foldDuplicateChannelName(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return ""
	}
	for range 3 {
		next := stripBracketedDuplicateTags(s)
		if next == s {
			break
		}
		s = next
	}

	rawParts := strings.Split(s, "|")
	partTokens := make([][]string, 0, len(rawParts))
	for _, part := range rawParts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Strip a leading "US:" or "CA -" country prefix when followed by a name.
		for _, sep := range []string{":", " - ", "- "} {
			if prefix, rest, ok := strings.Cut(part, sep); ok {
				pToks := splitNameTokens(prefix)
				rToks := splitNameTokens(rest)
				if len(pToks) == 1 && len(rToks) > 0 && isDuplicateRegionCode(strings.ToLower(pToks[0])) {
					part = rest
					break
				}
			}
		}
		toks := splitNameTokens(part)
		if len(toks) > 0 {
			partTokens = append(partTokens, toks)
		}
	}
	if len(partTokens) == 0 {
		return ""
	}

	isPureMarkerPart := func(toks []string) bool {
		for _, tok := range toks {
			lower := strings.ToLower(tok)
			_, isMarker, _ := classifyQualityToken(lower)
			if !isMarker && !isDuplicateRegionCode(lower) {
				return false
			}
		}
		return true
	}

	var tokens []string
	if len(partTokens) > 1 {
		nonMarkerCount := 0
		for _, toks := range partTokens {
			if !isPureMarkerPart(toks) {
				nonMarkerCount++
			}
		}
		for _, toks := range partTokens {
			if nonMarkerCount > 0 && isPureMarkerPart(toks) {
				continue
			}
			tokens = append(tokens, toks...)
		}
	} else {
		tokens = partTokens[0]
	}

	kept := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		lower := strings.ToLower(tok)
		_, isMarker, bareNum := classifyQualityToken(lower)
		if isMarker && !bareNum {
			continue
		}
		kept = append(kept, tok)
	}
	// Strip bare numeric resolutions ("1080", "720") only when another token remains.
	if len(kept) > 1 {
		filtered := kept[:0]
		for i, tok := range kept {
			_, isMarker, bareNum := classifyQualityToken(strings.ToLower(tok))
			if isMarker && bareNum && (len(filtered) > 0 || i+1 < len(kept)) {
				continue
			}
			filtered = append(filtered, tok)
		}
		kept = filtered
	}
	for len(kept) > 1 && isUpperDuplicateRegion(kept[0]) {
		kept = kept[1:]
	}
	for len(kept) > 1 && isUpperDuplicateRegion(kept[len(kept)-1]) {
		kept = kept[:len(kept)-1]
	}

	var out strings.Builder
	for _, tok := range kept {
		out.WriteString(strings.ToLower(tok))
	}
	return out.String()
}

type autoProgSlot struct {
	startMin int64
	title    string
}

func foldProgrammeTitle(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func loadAutoBackupSchedules(ctx context.Context, q queryer, keys []guideKey, from, until time.Time) (map[guideKey][]autoProgSlot, error) {
	out := make(map[guideKey][]autoProgSlot, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	fromMS, untilMS := from.UnixMilli(), until.UnixMilli()
	type progTitleOnly struct {
		Item struct {
			Title string `json:"title"`
		} `json:"item"`
	}
	for start := 0; start < len(keys); start += autoBackupGuideBatch {
		batch := keys[start:min(start+autoBackupGuideBatch, len(keys))]
		marks := make([]string, len(batch))
		args := make([]any, 0, len(batch)*2+2)
		for i, k := range batch {
			marks[i] = "(?,?)"
			args = append(args, k.source, k.guide)
		}
		args = append(args, fromMS, untilMS)
		rows, err := q.QueryContext(ctx,
			`SELECT source_id, channel_id, start_ms, data FROM livetv_programmes WHERE (source_id,channel_id) IN (`+strings.Join(marks, ",")+`) AND end_ms>? AND start_ms<? ORDER BY source_id, channel_id, start_ms`,
			args...,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k guideKey
			var startMS int64
			var raw []byte
			if err := rows.Scan(&k.source, &k.guide, &startMS, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			var decoded progTitleOnly
			if json.Unmarshal(raw, &decoded) != nil {
				continue
			}
			title := foldProgrammeTitle(decoded.Item.Title)
			if genericProgrammeTitles[title] {
				continue
			}
			slot := autoProgSlot{startMin: startMS / 60000, title: title}
			existing := out[k]
			if len(existing) > 0 && existing[len(existing)-1] == slot {
				continue
			}
			out[k] = append(existing, slot)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

func schedulesAlign6h(a, b []autoProgSlot) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	used := make([]bool, len(b))
	matched := 0
	for _, sa := range a {
		for j, sb := range b {
			if used[j] {
				continue
			}
			diff := sa.startMin - sb.startMin
			if diff < -1 {
				break
			}
			if diff <= 1 && sa.title == sb.title {
				used[j] = true
				matched++
				break
			}
		}
	}
	denom := max(len(a), len(b))
	return matched*5 >= denom*4
}

func channelsAreDuplicates(a, b Channel, schedules map[guideKey][]autoProgSlot) bool {
	ga, gb := strings.TrimSpace(a.GuideID), strings.TrimSpace(b.GuideID)
	if ga != "" && gb != "" && strings.EqualFold(ga, gb) {
		return true
	}
	if ga == "" || gb == "" {
		return false
	}
	sa := schedules[guideKey{source: guideSource(a), guide: ga}]
	sb := schedules[guideKey{source: guideSource(b), guide: gb}]
	return schedulesAlign6h(sa, sb)
}

func autoBackupInputID(donor Channel, donorSrc StreamSource) string {
	if isAddonInput(donorSrc.ID) {
		return AddonInputPrefix + "auto-" + stableID(donor.ID, donorSrc.ID)
	}
	return "auto-" + stableID(donor.ID, donorSrc.ID)
}

func defaultAutoBackupName(donor Channel) string {
	num := strings.TrimSpace(donor.Number)
	name := strings.TrimSpace(donor.Name)
	label := name
	if num != "" && name != "" {
		label = "#" + num + " " + name
	} else if num != "" {
		label = "#" + num
	}
	if label == "" {
		label = "Automatic backup"
	}
	if len(label) > 180 {
		label = strings.TrimSpace(label[:180])
	}
	return label
}

func donorPrimaryStream(donor Channel) (StreamSource, bool) {
	for _, src := range donor.StreamSources {
		if src.MergedFrom != "" || src.AutoFrom != "" || !src.Enabled {
			continue
		}
		src.URL = strings.TrimSpace(src.URL)
		if validStreamURL(src.URL) {
			return src, true
		}
	}
	rawURL := strings.TrimSpace(donor.StreamURL)
	if !validStreamURL(rawURL) {
		return StreamSource{}, false
	}
	id := stableID(donor.ID, "primary")
	if donor.SourceID != "" {
		id = stableID(donor.ID, "playlist")
	}
	return StreamSource{
		ID:      id,
		Name:    "Primary stream",
		URL:     rawURL,
		Enabled: true,
	}, true
}

func syncAutoDonorInput(donor Channel, donorSrc StreamSource, prevByDonor map[string]StreamSource, ownerEdited bool) StreamSource {
	id := autoBackupInputID(donor, donorSrc)
	defaultName := defaultAutoBackupName(donor)
	if existing, ok := prevByDonor[donor.ID]; ok {
		name := existing.Name
		if !ownerEdited || name == "" {
			name = defaultName
		}
		return StreamSource{
			ID:           id,
			Name:         name,
			URL:          donorSrc.URL,
			Enabled:      existing.Enabled,
			AuthSourceID: donor.SourceID,
			AutoFrom:     donor.ID,
			AutoNumber:   donor.Number,
			Headers:      CleanInputHeaders(donorSrc.Headers),
		}
	}
	return StreamSource{
		ID:           id,
		Name:         defaultName,
		URL:          donorSrc.URL,
		Enabled:      true,
		AuthSourceID: donor.SourceID,
		AutoFrom:     donor.ID,
		AutoNumber:   donor.Number,
		Headers:      CleanInputHeaders(donorSrc.Headers),
	}
}

func (s *Service) cachedStreamHeight(rawURL string, headers map[string]string) int {
	if s == nil || rawURL == "" {
		return 0
	}
	key := rawURL
	if len(headers) != 0 {
		hs := make(map[string][]string, len(headers))
		for k, v := range headers {
			hs[k] = []string{v}
		}
		encoded, _ := json.Marshal(hs)
		key = stableID(rawURL, string(encoded))
	}
	now := s.o.Now()
	s.probeMu.Lock()
	entry, ok := s.probeCache[key]
	s.probeMu.Unlock()
	if !ok || (!entry.expires.IsZero() && now.After(entry.expires)) {
		return 0
	}
	if v := entry.info.Video(); v != nil && v.Height > 0 {
		return v.Height
	}
	return 0
}

func heightToStep(h int) int {
	switch {
	case h >= 2000:
		return 6
	case h >= 1000:
		return 4
	case h >= 700:
		return 3
	case h >= 540:
		return 1
	default:
		return 0
	}
}

func channelResolutionStep(s *Service, c Channel, src StreamSource) (step int, known bool) {
	if h := s.cachedStreamHeight(src.URL, src.Headers); h > 0 {
		return heightToStep(h), true
	}
	tier, _ := streamQualityTier(c.Name, c.TvgName, c.Group, src.Name)
	switch tier {
	case qualityTier4K:
		return 6, true
	case qualityTierFHD:
		return 4, true
	case qualityTierHD:
		return 3, true
	case qualityTierSD:
		return 0, true
	default:
		return 0, false
	}
}

func stepPreferenceRank(step int, known bool) int {
	if !known {
		return 2
	}
	switch step {
	case 4:
		return 0
	case 3:
		return 1
	case 6:
		return 3
	case 1:
		return 4
	default:
		return 5
	}
}

func resolutionDistance(targetStep int, targetKnown bool, candStep int, candKnown bool) (dist int, pref int) {
	pref = stepPreferenceRank(candStep, candKnown)
	if !targetKnown {
		return pref, pref
	}
	if !candKnown {
		return 5, pref
	}
	d := targetStep - candStep
	if d < 0 {
		d = -d
	}
	return d * 2, pref
}

// autoBackupHistory combines evidence recorded on target's backup input with
// evidence recorded on the duplicate channel's own primary input, without
// taking locks or touching the database.
func (s *Service) autoBackupHistory(targetID string, src StreamSource) SourceHistory {
	if s == nil {
		return SourceHistory{}
	}
	out, hasTarget := s.sourceHistory(targetID, src)
	if src.AutoFrom == "" {
		return out
	}
	var donorHist SourceHistory
	hasDonor := false
	for _, candID := range []string{
		stableID(src.AutoFrom, "playlist"),
		stableID(src.AutoFrom, "primary"),
		AddonInputID(src.AutoFrom, 0),
	} {
		if h, ok := s.sourceHistory(src.AutoFrom, StreamSource{ID: candID, URL: src.URL}); ok {
			donorHist = h
			hasDonor = true
			break
		}
	}
	if !hasDonor {
		return out
	}
	if !hasTarget {
		return donorHist
	}
	failures := out.Failures
	if donorHist.Updated.After(out.Updated) {
		failures = donorHist.Failures
	}
	out.Failures = failures
	out.OpenFailures += donorHist.OpenFailures
	out.MediaSeconds += donorHist.MediaSeconds
	out.Stalls += donorHist.Stalls
	out.DamageEvents += donorHist.DamageEvents
	out.Reconnects += donorHist.Reconnects
	return out
}

func compareAutoBackupHealth(ha, hb SourceHistory) int {
	if ha.Failures != hb.Failures {
		if ha.Failures < hb.Failures {
			return -1
		}
		return 1
	}
	sa := int64(math.Round(ha.StallsPerHour() * 10))
	sb := int64(math.Round(hb.StallsPerHour() * 10))
	if sa != sb {
		if sa < sb {
			return -1
		}
		return 1
	}
	if ha.OpenFailures != hb.OpenFailures {
		if ha.OpenFailures < hb.OpenFailures {
			return -1
		}
		return 1
	}
	da := int64(math.Round(ha.DamagePerHour() * 10))
	db := int64(math.Round(hb.DamagePerHour() * 10))
	if da != db {
		if da < db {
			return -1
		}
		return 1
	}
	return 0
}

func (s *Service) compareAutoBackupCandidates(target Channel, targetStep int, targetKnown bool, aSrc StreamSource, aDonor Channel, bSrc StreamSource, bDonor Channel, sourceRanks map[string]int) int {
	ha := s.autoBackupHistory(target.ID, aSrc)
	hb := s.autoBackupHistory(target.ID, bSrc)
	if cmp := compareAutoBackupHealth(ha, hb); cmp != 0 {
		return cmp
	}

	aStep, aKnown := channelResolutionStep(s, aDonor, aSrc)
	bStep, bKnown := channelResolutionStep(s, bDonor, bSrc)
	aDist, aPref := resolutionDistance(targetStep, targetKnown, aStep, aKnown)
	bDist, bPref := resolutionDistance(targetStep, targetKnown, bStep, bKnown)
	if aDist != bDist {
		if aDist < bDist {
			return -1
		}
		return 1
	}
	if aPref != bPref {
		if aPref < bPref {
			return -1
		}
		return 1
	}

	aSame := target.SourceID != "" && aDonor.SourceID == target.SourceID
	bSame := target.SourceID != "" && bDonor.SourceID == target.SourceID
	if aSame != bSame {
		if aSame {
			return -1
		}
		return 1
	}

	aClean := ha.MediaSeconds > 0 && ha.Stalls == 0 && ha.Failures == 0 && ha.OpenFailures == 0
	bClean := hb.MediaSeconds > 0 && hb.Stalls == 0 && hb.Failures == 0 && hb.OpenFailures == 0
	if aClean != bClean {
		if aClean {
			return -1
		}
		return 1
	}

	if sourceRanks != nil {
		ra, oka := sourceRanks[aDonor.SourceID]
		rb, okb := sourceRanks[bDonor.SourceID]
		if oka && okb && ra != rb {
			if ra < rb {
				return -1
			}
			return 1
		}
	}

	if cmp := parseNumberKey(aDonor.Number).compare(parseNumberKey(bDonor.Number)); cmp != 0 {
		return cmp
	}
	return strings.Compare(aDonor.ID, bDonor.ID)
}

// compareRuntimeAutoBackups orders two same-class automatic backups at tune or
// failover time using lock-free stall history and cached probes, falling back
// to persisted priority when equal.
func (s *Service) compareRuntimeAutoBackups(c Channel, a, b StreamSource) int {
	ha := s.autoBackupHistory(c.ID, a)
	hb := s.autoBackupHistory(c.ID, b)
	if cmp := compareAutoBackupHealth(ha, hb); cmp != 0 {
		return cmp
	}
	aDonor, okA := s.cachedChannelByID(a.AutoFrom)
	bDonor, okB := s.cachedChannelByID(b.AutoFrom)
	var primarySrc StreamSource
	if len(c.StreamSources) > 0 {
		primarySrc = c.StreamSources[0]
	}
	targetStep, targetKnown := channelResolutionStep(s, c, primarySrc)
	aStep, aKnown := channelResolutionStep(s, aDonor, a)
	bStep, bKnown := channelResolutionStep(s, bDonor, b)
	if (okA && okB) || (aKnown && bKnown) {
		aDist, aPref := resolutionDistance(targetStep, targetKnown, aStep, aKnown)
		bDist, bPref := resolutionDistance(targetStep, targetKnown, bStep, bKnown)
		if aDist != bDist {
			if aDist < bDist {
				return -1
			}
			return 1
		}
		if aPref != bPref {
			if aPref < bPref {
				return -1
			}
			return 1
		}
		aSrcID := a.AuthSourceID
		if aSrcID == "" && okA {
			aSrcID = aDonor.SourceID
		}
		bSrcID := b.AuthSourceID
		if bSrcID == "" && okB {
			bSrcID = bDonor.SourceID
		}
		aSame := c.SourceID != "" && aSrcID == c.SourceID
		bSame := c.SourceID != "" && bSrcID == c.SourceID
		if aSame != bSame {
			if aSame {
				return -1
			}
			return 1
		}
	}
	if a.Priority < b.Priority {
		return -1
	}
	if a.Priority > b.Priority {
		return 1
	}
	return 0
}

// reconcileSavedAutoBackups updates owner suppression and ordering flags on a
// single saved channel without scanning other channels.
func reconcileSavedAutoBackups(c *Channel, old Channel, hasOld bool) {
	if c.Kind != "iptv" {
		c.SuppressedAutoBackups = nil
		c.OwnerStreamSources = false
		return
	}
	suppressed := make(map[string]bool, len(c.SuppressedAutoBackups)+len(old.SuppressedAutoBackups))
	for _, id := range old.SuppressedAutoBackups {
		if id = strings.TrimSpace(id); id != "" && id != c.ID {
			suppressed[id] = true
		}
	}
	for _, id := range c.SuppressedAutoBackups {
		if id = strings.TrimSpace(id); id != "" && id != c.ID {
			suppressed[id] = true
		}
	}
	if hasOld {
		c.OwnerStreamSources = c.OwnerStreamSources || old.OwnerStreamSources
		oldAutoByDonor := make(map[string]StreamSource)
		var oldAutoOrder []string
		for _, src := range old.StreamSources {
			if src.AutoFrom != "" {
				oldAutoByDonor[src.AutoFrom] = src
				oldAutoOrder = append(oldAutoOrder, src.AutoFrom)
			}
		}
		// Only treat missing auto backups as owner-removed when the caller
		// supplied an explicit streamSources list.
		if len(c.StreamSources) > 0 {
			presentDonors := make(map[string]bool, len(c.StreamSources))
			var newAutoOrder []string
			for i := range c.StreamSources {
				src := &c.StreamSources[i]
				if src.AutoFrom == "" {
					continue
				}
				if prev, ok := oldAutoByDonor[src.AutoFrom]; ok {
					if strings.TrimSpace(src.URL) != strings.TrimSpace(prev.URL) {
						// Owner replaced the URL: convert to a manual source and suppress the auto donor.
						suppressed[src.AutoFrom] = true
						src.AutoFrom = ""
						src.AutoNumber = ""
						c.OwnerStreamSources = true
						continue
					}
					if src.Enabled != prev.Enabled || strings.TrimSpace(src.Name) != strings.TrimSpace(prev.Name) {
						c.OwnerStreamSources = true
					}
				}
				presentDonors[src.AutoFrom] = true
				newAutoOrder = append(newAutoOrder, src.AutoFrom)
			}
			if !hasInputMergeRules(*c) {
				for donorID := range oldAutoByDonor {
					if !presentDonors[donorID] {
						suppressed[donorID] = true
					}
				}
			}
			// Detect owner reordering of surviving auto backups or placing an auto backup first.
			var filteredOldOrder []string
			for _, donorID := range oldAutoOrder {
				if presentDonors[donorID] {
					filteredOldOrder = append(filteredOldOrder, donorID)
				}
			}
			if !slices.Equal(filteredOldOrder, newAutoOrder) {
				c.OwnerStreamSources = true
			}
			if len(c.StreamSources) > 0 && c.StreamSources[0].AutoFrom != "" {
				c.OwnerStreamSources = true
			}
		}
	}
	manualCount := 0
	for _, src := range c.StreamSources {
		if src.AutoFrom == "" && src.MergedFrom == "" {
			manualCount++
		}
	}
	if manualCount > 1 {
		c.OwnerStreamSources = true
	}
	if len(suppressed) == 0 {
		c.SuppressedAutoBackups = nil
		return
	}
	list := make([]string, 0, len(suppressed))
	for id := range suppressed {
		list = append(list, id)
	}
	sort.Strings(list)
	c.SuppressedAutoBackups = list
}

func pruneAutoSourcesFromChannel(c *Channel) {
	if !hasAutoStreamSources(*c) {
		return
	}
	kept := make([]StreamSource, 0, len(c.StreamSources))
	for _, src := range c.StreamSources {
		if src.AutoFrom == "" {
			kept = append(kept, src)
		}
	}
	if len(kept) == 0 && validStreamURL(strings.TrimSpace(c.StreamURL)) {
		c.StreamSources = nil
		_ = normalizeStreamSources(c)
		return
	}
	if len(kept) > 0 {
		hasEnabled := false
		for _, src := range kept {
			if src.Enabled {
				hasEnabled = true
				break
			}
		}
		if c.Enabled && !hasEnabled {
			kept[0].Enabled = true
		}
		c.StreamSources = kept
		_ = normalizeStreamSources(c)
	}
}

// reconcileAutoBackups evaluates duplicate IPTV channels across cs and updates
// automatic backup stream sources in place, returning indices of changed rows.
func (s *Service) reconcileAutoBackups(cs []Channel, srcs []Source, schedules map[guideKey][]autoProgSlot) []int {
	before := make([][]byte, len(cs))
	for i := range cs {
		before[i], _ = json.Marshal(cs[i])
	}

	disabledSources := make(map[string]bool, len(srcs))
	for _, src := range srcs {
		if src.Disabled {
			disabledSources[src.ID] = true
		}
	}
	_, sourceRanks := buildSourcePriorityMaps(srcs)

	buckets := map[string][]int{}
	for i := range cs {
		c := &cs[i]
		if c.Kind != "iptv" {
			continue
		}
		if !c.Enabled || c.MergedInto != "" || hasInputMergeRules(*c) || disabledSources[c.SourceID] {
			if c.MergedInto != "" || hasInputMergeRules(*c) || disabledSources[c.SourceID] {
				pruneAutoSourcesFromChannel(c)
			}
			continue
		}
		folded := foldDuplicateChannelName(c.Name)
		if folded == "" {
			pruneAutoSourcesFromChannel(c)
			continue
		}
		buckets[folded] = append(buckets[folded], i)
	}

	for _, bucket := range buckets {
		if len(bucket) < 2 {
			pruneAutoSourcesFromChannel(&cs[bucket[0]])
			continue
		}
		for _, ti := range bucket {
			target := &cs[ti]
			suppressed := make(map[string]bool, len(target.SuppressedAutoBackups))
			for _, id := range target.SuppressedAutoBackups {
				suppressed[id] = true
			}
			prevByDonor := make(map[string]StreamSource, len(target.StreamSources))
			for _, src := range target.StreamSources {
				if src.AutoFrom != "" {
					prevByDonor[src.AutoFrom] = src
				}
			}

			type autoCand struct {
				src   StreamSource
				donor Channel
			}
			var matched []autoCand
			for _, di := range bucket {
				if di == ti {
					continue
				}
				donor := cs[di]
				if suppressed[donor.ID] || !channelsAreDuplicates(*target, donor, schedules) {
					continue
				}
				dSrc, ok := donorPrimaryStream(donor)
				if !ok {
					continue
				}
				matched = append(matched, autoCand{
					src:   syncAutoDonorInput(donor, dSrc, prevByDonor, target.OwnerStreamSources),
					donor: donor,
				})
			}

			if len(matched) == 0 {
				pruneAutoSourcesFromChannel(target)
				continue
			}

			// Ensure target has its own normalized base stream before attaching backups.
			if len(target.StreamSources) == 0 {
				if err := normalizeStreamSources(target); err != nil {
					continue
				}
			}
			var primarySrc StreamSource
			for _, src := range target.StreamSources {
				if src.AutoFrom == "" && src.MergedFrom == "" {
					primarySrc = src
					break
				}
			}
			targetStep, targetKnown := channelResolutionStep(s, *target, primarySrc)

			sort.SliceStable(matched, func(i, j int) bool {
				return s.compareAutoBackupCandidates(*target, targetStep, targetKnown, matched[i].src, matched[i].donor, matched[j].src, matched[j].donor, sourceRanks) < 0
			})

			matchedByDonor := make(map[string]StreamSource, len(matched))
			for _, m := range matched {
				matchedByDonor[m.donor.ID] = m.src
			}

			seenIDs := make(map[string]bool, MaxChannelInputs)
			seenURLs := make(map[string]bool, MaxChannelInputs)
			nextSources := make([]StreamSource, 0, min(len(target.StreamSources)+len(matched), MaxChannelInputs))

			addSource := func(src StreamSource) {
				if len(nextSources) >= MaxChannelInputs {
					return
				}
				src.URL = strings.TrimSpace(src.URL)
				if !validStreamURL(src.URL) || seenIDs[src.ID] || seenURLs[src.URL] {
					return
				}
				seenIDs[src.ID] = true
				seenURLs[src.URL] = true
				nextSources = append(nextSources, src)
			}

			if target.OwnerStreamSources {
				// Owner-edited streamSources win: preserve exact manual + surviving
				// auto order, then append any newly discovered duplicate backups.
				for _, existing := range target.StreamSources {
					if existing.AutoFrom == "" {
						addSource(existing)
						continue
					}
					if synced, ok := matchedByDonor[existing.AutoFrom]; ok {
						addSource(synced)
					}
				}
				for _, m := range matched {
					addSource(m.src)
				}
			} else {
				// Default precedence: channel's own primary stream(s) first, then
				// ranked automatic duplicate backups.
				for _, existing := range target.StreamSources {
					if existing.AutoFrom == "" {
						addSource(existing)
					}
				}
				for _, m := range matched {
					addSource(m.src)
				}
			}

			if len(nextSources) == 0 {
				continue
			}
			hasEnabled := false
			for _, src := range nextSources {
				if src.Enabled {
					hasEnabled = true
					break
				}
			}
			if target.Enabled && !hasEnabled {
				nextSources[0].Enabled = true
			}
			for i := range nextSources {
				nextSources[i].Priority = i
			}
			target.StreamSources = nextSources
			_ = normalizeStreamSources(target)
		}
	}

	var changed []int
	for i := range cs {
		after, err := json.Marshal(cs[i])
		if err != nil || !bytes.Equal(before[i], after) {
			changed = append(changed, i)
		}
	}
	return changed
}

// reconcileAutoBackupsTx computes duplicate-channel backup matches inside tx
// using bounded programme-query and channel-upsert batches. When inPlace is
// non-nil, matching elements in inPlace are updated to reflect persisted rows.
func (s *Service) reconcileAutoBackupsTx(ctx context.Context, tx *sql.Tx, writeBatch int, inPlace []Channel) (bool, []string, error) {
	all, err := channels(ctx, tx)
	if err != nil {
		return false, nil, err
	}
	if len(all) == 0 {
		return false, nil, nil
	}
	srcs, err := s.sourcesTx(ctx, tx)
	if err != nil {
		return false, nil, err
	}
	disabledSources := make(map[string]bool, len(srcs))
	for _, src := range srcs {
		if src.Disabled {
			disabledSources[src.ID] = true
		}
	}

	// Collect 6h schedule keys only for multi-channel folded-name buckets that
	// include pairs with distinct non-empty GuideIDs.
	buckets := map[string][]Channel{}
	for _, c := range all {
		if c.Kind != "iptv" || !c.Enabled || c.MergedInto != "" || hasInputMergeRules(c) || disabledSources[c.SourceID] {
			continue
		}
		if folded := foldDuplicateChannelName(c.Name); folded != "" {
			buckets[folded] = append(buckets[folded], c)
		}
	}
	wantedKeys := map[guideKey]bool{}
	var keyList []guideKey
	for _, bucket := range buckets {
		if len(bucket) < 2 {
			continue
		}
		needSchedule := false
		firstGuide := strings.ToLower(strings.TrimSpace(bucket[0].GuideID))
		for _, c := range bucket[1:] {
			g := strings.ToLower(strings.TrimSpace(c.GuideID))
			if firstGuide == "" || g == "" || g != firstGuide {
				needSchedule = true
				break
			}
		}
		if !needSchedule {
			continue
		}
		for _, c := range bucket {
			gid := strings.TrimSpace(c.GuideID)
			if gid == "" {
				continue
			}
			k := guideKey{source: guideSource(c), guide: gid}
			if !wantedKeys[k] {
				wantedKeys[k] = true
				keyList = append(keyList, k)
			}
		}
	}

	now := s.o.Now()
	schedules, err := loadAutoBackupSchedules(ctx, tx, keyList, now, now.Add(autoBackupWindow))
	if err != nil {
		return false, nil, err
	}

	prevStreamURL := make(map[string]string, len(all))
	for _, c := range all {
		prevStreamURL[c.ID] = c.StreamURL
	}

	changedIdx := s.reconcileAutoBackups(all, srcs, schedules)
	if len(changedIdx) == 0 {
		return false, nil, nil
	}

	if writeBatch <= 0 {
		writeBatch = autoBackupWriteBatch
	}
	updatedByID := make(map[string]Channel, len(changedIdx))
	var invalidated []string
	for start := 0; start < len(changedIdx); start += writeBatch {
		chunk := changedIdx[start:min(start+writeBatch, len(changedIdx))]
		marks := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)*2)
		for i, idx := range chunk {
			updated := all[idx]
			b, err := json.Marshal(updated)
			if err != nil {
				return false, nil, err
			}
			marks[i] = "(?,?)"
			args = append(args, updated.ID, string(b))
			updatedByID[updated.ID] = updated
			if prevStreamURL[updated.ID] != updated.StreamURL {
				invalidated = append(invalidated, updated.ID)
			}
		}
		q := `INSERT INTO livetv_channels(id,data) VALUES ` + strings.Join(marks, ",") + ` ON CONFLICT(id) DO UPDATE SET data=excluded.data`
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return false, nil, err
		}
	}
	for i := range inPlace {
		if u, ok := updatedByID[inPlace[i].ID]; ok {
			inPlace[i] = u
		}
	}
	return true, invalidated, nil
}

func (s *Service) cachedChannelByID(id string) (Channel, bool) {
	if s == nil || id == "" {
		return Channel{}, false
	}
	snap := &s.chanSnap
	snap.mu.Lock()
	defer snap.mu.Unlock()
	if idx, ok := snap.byID[id]; ok && idx >= 0 && idx < len(snap.list) {
		return snap.list[idx], true
	}
	return Channel{}, false
}

func (s *Service) inputChannelNumber(c Channel, src StreamSource) string {
	donorID := src.AutoFrom
	if donorID == "" {
		donorID = src.MergedFrom
	}
	if donorID != "" {
		if donor, ok := s.cachedChannelByID(donorID); ok && donor.Number != "" {
			return donor.Number
		}
		if src.AutoNumber != "" {
			return src.AutoNumber
		}
	}
	return c.Number
}

// duplicateFailoverLogAttrs returns structured channel-number log fields when
// playback fails over between duplicate channel streams.
func (s *Service) duplicateFailoverLogAttrs(c Channel, from, to StreamSource) []any {
	if from.ID == "" || to.ID == "" || from.ID == to.ID {
		return nil
	}
	fromNum := strings.TrimPrefix(strings.TrimSpace(s.inputChannelNumber(c, from)), "#")
	toNum := strings.TrimPrefix(strings.TrimSpace(s.inputChannelNumber(c, to)), "#")
	if from.AutoFrom == "" && to.AutoFrom == "" && (fromNum == "" || toNum == "" || fromNum == toNum) {
		return nil
	}
	var attrs []any
	if chNum := strings.TrimPrefix(strings.TrimSpace(c.Number), "#"); chNum != "" {
		attrs = append(attrs, "channel_number", "#"+chNum)
	}
	if fromNum != "" {
		attrs = append(attrs, "from_channel", "#"+fromNum)
	}
	if toNum != "" {
		attrs = append(attrs, "to_channel", "#"+toNum)
	}
	return attrs
}
