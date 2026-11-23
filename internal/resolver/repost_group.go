package resolver

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

type fallbackValidation struct {
	trace int
	rel   core.Release
	val   validation
}

// repostObfuscationSuffixes are common Usenet/indexer repost and obfuscation
// suffixes stripped when normalising filenames for near-duplicate grouping.
var repostObfuscationSuffixes = map[string]bool{
	"obfuscated":  true,
	"scrambled":   true,
	"postbot":     true,
	"xpost":       true,
	"nzbgeek":     true,
	"asrequested": true,
	"buymore":     true,
	"chamele0n":   true,
	"rakuv":       true,
	"rakuv02":     true,
	"rakuvfinhel": true,
	"whiterev":    true,
	"wrteam":      true,
	"captcha":     true,
	"z0ids3n":     true,
	"repost":      true,
	"unpacked":    true,
	"newzbin":     true,
	"oznzb":       true,
	"dognzb":      true,
	"drunkenslug": true,
	"abnzb":       true,
	"rarbg":       true,
	"rartv":       true,
	"eztv":        true,
	"ettv":        true,
	"tgx":         true,
}

var mediaExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".m4v": true,
	".ts": true, ".m2ts": true, ".wmv": true, ".mov": true,
	".webm": true, ".iso": true, ".nzb": true, ".rar": true, ".par2": true,
}

// repostSizesWithinTenPercent reports whether sizes a and b are within ±10%
// (or both zero when size is unknown).
func repostSizesWithinTenPercent(a, b int64) bool {
	if a <= 0 || b <= 0 {
		return a == 0 && b == 0
	}
	hi, lo := a, b
	if lo > hi {
		hi, lo = lo, hi
	}
	// (hi - lo) / hi <= 0.10  <=>  (hi - lo) * 10 <= hi
	diff := hi - lo
	if diff <= hi/10 {
		return true
	}
	return diff*10 <= hi
}

// normalizedReleaseFilename normalises a release name for near-duplicate repost
// grouping: strips file extensions, leading/trailing group tags, obfuscation
// suffixes, case, and punctuation. Returns "" if the remaining name is empty
// or obfuscated.
func normalizedReleaseFilename(name string, parsedGroup string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return ""
	}
	// Strip known file/archive extensions at the end.
	for {
		dot := strings.LastIndexByte(s, '.')
		if dot <= 0 {
			break
		}
		if mediaExtensions[strings.ToLower(s[dot:])] {
			s = strings.TrimSpace(s[:dot])
			continue
		}
		break
	}
	// Strip leading bracketed group tags ("[SubsPlease] ...", "[HorribleSubs]...").
	for strings.HasPrefix(s, "[") {
		end := strings.IndexByte(s, ']')
		if end <= 0 || end >= len(s)-1 {
			break
		}
		s = strings.TrimSpace(s[end+1:])
	}
	// Strip trailing bracketed/braced suffixes ("[rartv]", etc., preserving tech tokens).
	for (strings.HasSuffix(s, "]") || strings.HasSuffix(s, "}")) && len(s) > 2 {
		open := byte('[')
		if strings.HasSuffix(s, "}") {
			open = '{'
		}
		idx := strings.LastIndexByte(s, open)
		if idx <= 0 {
			break
		}
		inner := strings.ToLower(strings.TrimSpace(s[idx+1 : len(s)-1]))
		if isProtectedTechToken(inner) {
			break
		}
		s = strings.TrimSpace(s[:idx])
	}
	// Strip trailing dash-delimited obfuscation suffixes and group tags.
	groupLower := strings.ToLower(strings.TrimSpace(parsedGroup))
	strippedGroup := false
	for {
		s = strings.TrimSpace(s)
		dash := strings.LastIndexByte(s, '-')
		if dash <= 0 || dash >= len(s)-1 {
			break
		}
		tail := strings.TrimSpace(s[dash+1:])
		tailLower := strings.ToLower(tail)
		if tailLower == "" {
			s = s[:dash]
			continue
		}
		if isProtectedTechToken(tailLower) || isAudioChannelSuffix(s[:dash], tailLower) {
			break
		}
		if repostObfuscationSuffixes[tailLower] || releaseparse.IsObfuscated(tail) {
			s = strings.TrimSpace(s[:dash])
			continue
		}
		if groupLower != "" && tailLower == groupLower {
			s = strings.TrimSpace(s[:dash])
			strippedGroup = true
			continue
		}
		// Scene/P2P group suffix at the end after technical tokens (e.g. "...H.264-NTb").
		if !strippedGroup && looksLikeTrailingGroupTag(s[:dash], tailLower) {
			s = strings.TrimSpace(s[:dash])
			strippedGroup = true
			continue
		}
		break
	}
	// Strip trailing dot/space-separated obfuscation tokens if any remain.
	rawTokens := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for len(rawTokens) > 2 {
		last := rawTokens[len(rawTokens)-1]
		lastLower := strings.ToLower(last)
		if repostObfuscationSuffixes[lastLower] || (!isProtectedTechToken(lastLower) && releaseparse.IsObfuscated(last)) {
			rawTokens = rawTokens[:len(rawTokens)-1]
			continue
		}
		break
	}
	if len(rawTokens) == 0 {
		return ""
	}
	for i := range rawTokens {
		rawTokens[i] = strings.ToLower(rawTokens[i])
	}
	joined := strings.Join(rawTokens, " ")
	if len(joined) < 3 || releaseparse.IsObfuscated(joined) {
		return ""
	}
	return joined
}

func isProtectedTechToken(tok string) bool {
	switch tok {
	case "dl", "rip", "hd", "ma", "x", "es", "hr", "264", "265", "x264", "x265", "h264", "h265",
		"hevc", "avc", "av1", "aac", "ac3", "eac3", "ddp", "dd", "truehd", "atmos", "dts", "dtshd",
		"flac", "opus", "mp3", "pcm", "lpcm", "hdr", "hdr10", "hdr10plus", "dv", "dovi",
		"480p", "576p", "720p", "1080p", "1080i", "2160p", "4k", "uhd",
		"web", "webdl", "webrip", "bluray", "bdrip", "brrip", "remux", "hdtv", "dvdrip",
		"proper", "repack", "rerip", "real", "multi", "dual", "dubbed", "subbed", "uncut",
		"extended", "unrated", "directors", "imax", "open", "matte", "theatrical", "remastered":
		return true
	}
	if len(tok) >= 2 && (tok[0] == 's' || tok[0] == 'e') && allDigits(tok[1:]) {
		return true
	}
	if allDigits(tok) {
		return true
	}
	return false
}

func isAudioChannelSuffix(prefix, tail string) bool {
	if !allDigits(tail) {
		return false
	}
	p := strings.ToLower(strings.TrimSpace(prefix))
	return strings.HasSuffix(p, "5.1") || strings.HasSuffix(p, "7.1") || strings.HasSuffix(p, "2.0") ||
		strings.HasSuffix(p, "h") || strings.HasSuffix(p, "x")
}

func looksLikeTrailingGroupTag(prefix, tail string) bool {
	if len(tail) < 2 || len(tail) > 20 || allDigits(tail) {
		return false
	}
	// Only treat "-Group" as a release group when the prefix already carries
	// technical release tokens (resolution, source, or codec), avoiding
	// stripping hyphenated movie/episode titles like "Spider-Man".
	pl := strings.ToLower(prefix)
	for _, marker := range []string{
		"2160p", "1080p", "720p", "480p", "576p",
		"web-dl", "webdl", "webrip", "bluray", "blu-ray", "bdrip", "remux", "hdtv", "dvdrip",
		"x264", "x265", "h264", "h265", "h.264", "h.265", "hevc", "av1", "xvid",
	} {
		if strings.Contains(pl, marker) {
			return true
		}
	}
	return false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func normalizeAlphaNum(s string) string {
	toks := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return strings.Join(toks, " ")
}

// parsedRepostSignature builds the attribute grouping key over
// (title, year, season/episode, resolution, source, codec, audio).
// Returns "" when the release lacks a usable parsed title or technical info.
func parsedRepostSignature(rel core.Release) string {
	info := rel.Info
	if info.Title == "" && info.Height == 0 && info.Source == core.SourceUnknown {
		info = releaseparse.Parse(rel.Name)
	}
	title := normalizeAlphaNum(info.Title)
	if title == "" || releaseparse.IsObfuscated(info.Title) {
		return ""
	}
	if info.Height == 0 && info.Source == core.SourceUnknown && info.VideoCodec == "" && info.AudioCodec == "" {
		return ""
	}
	seasons := slices.Clone(info.Seasons)
	slices.Sort(seasons)
	episodes := slices.Clone(info.Episodes)
	slices.Sort(episodes)
	absEps := slices.Clone(info.AbsoluteEpisodes)
	slices.Sort(absEps)
	langs := make([]string, 0, len(info.Languages))
	for _, l := range info.Languages {
		if s := strings.ToLower(strings.TrimSpace(l)); s != "" {
			langs = append(langs, s)
		}
	}
	slices.Sort(langs)
	langs = slices.Compact(langs)

	targetKey := ""
	if rel.Target != nil && (rel.Target.Season > 0 || rel.Target.Episode > 0 || rel.Target.AbsoluteEpisode > 0) {
		targetKey = fmt.Sprintf("%d:%d:%d", rel.Target.Season, rel.Target.Episode, rel.Target.AbsoluteEpisode)
	}

	return fmt.Sprintf("attr|%s|y:%d|fs:%t|s:%v|e:%v|ae:%v|c:%d|t:%s|h:%d|src:%s|vc:%s|hdr:%t|dv:%t|ac:%s|atm:%t|ch:%s|lang:%s",
		title,
		info.Year,
		info.FullSeason,
		seasons,
		episodes,
		absEps,
		info.Cour,
		targetKey,
		info.Height,
		info.Source,
		strings.ToLower(info.VideoCodec),
		info.HDR10,
		info.DolbyVision,
		strings.ToLower(info.AudioCodec),
		info.Atmos,
		strings.ToLower(info.Channels),
		strings.Join(langs, ","),
	)
}

func repostSeasonEpisodeScope(rel core.Release) string {
	info := rel.Info
	if info.Title == "" && info.Height == 0 && info.Source == core.SourceUnknown {
		info = releaseparse.Parse(rel.Name)
	}
	seasons := slices.Clone(info.Seasons)
	slices.Sort(seasons)
	episodes := slices.Clone(info.Episodes)
	slices.Sort(episodes)
	absEps := slices.Clone(info.AbsoluteEpisodes)
	slices.Sort(absEps)
	targetKey := ""
	if rel.Target != nil && (rel.Target.Season > 0 || rel.Target.Episode > 0) {
		targetKey = strconv.Itoa(rel.Target.Season) + ":" + strconv.Itoa(rel.Target.Episode)
	}
	return fmt.Sprintf("fs:%t|s:%v|e:%v|ae:%v|t:%s", info.FullSeason, seasons, episodes, absEps, targetKey)
}

// repostGroupCompatible ensures only genuinely interchangeable candidates can
// share one failover slot and health-check group.
func repostGroupCompatible(a, b candidate) bool {
	if a.rel.Protocol != b.rel.Protocol || a.rel.Private != b.rel.Private {
		return false
	}
	if a.rel.OpensOnDemand() || b.rel.OpensOnDemand() {
		return false
	}
	if a.keys.sourceBand != b.keys.sourceBand || a.keys.addonOrder > 0 || b.keys.addonOrder > 0 {
		return false
	}
	switch a.rel.Protocol {
	case core.ProtocolTorrent:
		// Torrents with different info hashes are distinct swarms; only one may be cached.
		if a.rel.InfoHash == "" || !strings.EqualFold(a.rel.InfoHash, b.rel.InfoHash) {
			return false
		}
	case core.ProtocolHTTP:
		if a.rel.StreamURL == "" || a.rel.StreamURL != b.rel.StreamURL {
			return false
		}
	}
	if a.rel.Info.FullSeason != b.rel.Info.FullSeason || a.rel.Info.Edition != b.rel.Info.Edition {
		return false
	}
	if a.policy.Fallback != b.policy.Fallback ||
		a.fallback != b.fallback ||
		a.belowScore != b.belowScore ||
		a.starved != b.starved ||
		a.langOK != b.langOK ||
		a.langRank != b.langRank ||
		a.audioUnverified != b.audioUnverified ||
		a.identityWeak != b.identityWeak ||
		a.identityConflict != b.identityConflict ||
		a.bandwidth != b.bandwidth {
		return false
	}
	return repostSeasonEpisodeScope(a.rel) == repostSeasonEpisodeScope(b.rel)
}

// repostCandidatesMatch reports whether a and b belong to the same repost group.
func repostCandidatesMatch(a, b candidate) bool {
	if !repostGroupCompatible(a, b) {
		return false
	}
	if a.rel.Protocol == core.ProtocolHTTP {
		return a.rel.StreamURL != "" && a.rel.StreamURL == b.rel.StreamURL
	}
	if a.rel.Protocol == core.ProtocolUsenet {
		if cidA, cidB := a.rel.EffectiveContentID(), b.rel.EffectiveContentID(); cidA != "" && cidA == cidB {
			return true
		}
	}
	if !repostSizesWithinTenPercent(a.rel.Size, b.rel.Size) {
		return false
	}
	fnA := normalizedReleaseFilename(a.rel.Name, a.rel.Info.Group)
	fnB := normalizedReleaseFilename(b.rel.Name, b.rel.Info.Group)
	if fnA != "" && fnA == fnB {
		return true
	}
	sigA := parsedRepostSignature(a.rel)
	sigB := parsedRepostSignature(b.rel)
	return sigA != "" && sigA == sigB
}

// groupRepostCandidates collapses near-duplicate reposts in ranked order: the
// highest-ranked member leads each group and the remaining members become quiet
// fallbacks inside leader.fallbacks. Explicit pins and viewer picks stay
// standalone.
func groupRepostCandidates(cands []candidate, protectedIDs ...string) []candidate {
	if len(cands) < 2 {
		return cands
	}
	protected := map[string]bool{}
	for _, id := range protectedIDs {
		if id != "" {
			protected[id] = true
		}
	}

	type bucketKey struct {
		key    string
		bucket int64
	}
	byContent := map[string][]int{}
	byFile := map[bucketKey][]int{}
	byAttr := map[bucketKey][]int{}

	leaders := make([]candidate, 0, len(cands))

	findLeader := func(c candidate, fnKey, attrKey, cid string, bucket int64) int {
		bestIdx := -1
		consider := func(idxs []int) {
			for _, idx := range idxs {
				if bestIdx >= 0 && idx >= bestIdx {
					continue
				}
				if repostCandidatesMatch(leaders[idx], c) {
					bestIdx = idx
				}
			}
		}
		if cid != "" {
			consider(byContent[cid])
		}
		for _, b := range []int64{bucket - 1, bucket, bucket + 1} {
			if b < 0 {
				continue
			}
			if fnKey != "" {
				consider(byFile[bucketKey{key: fnKey, bucket: b}])
			}
			if attrKey != "" {
				consider(byAttr[bucketKey{key: attrKey, bucket: b}])
			}
		}
		return bestIdx
	}

	for _, c := range cands {
		if protected[c.rel.ID] || c.rel.OpensOnDemand() {
			leaders = append(leaders, c)
			continue
		}
		cid := ""
		if c.rel.Protocol == core.ProtocolUsenet {
			cid = c.rel.EffectiveContentID()
		} else if c.rel.Protocol == core.ProtocolHTTP && c.rel.StreamURL != "" {
			cid = "http:" + c.rel.StreamURL
		}
		bucket := core.ReleaseSizeBucket(c.rel.Size)
		fnKey := ""
		if fn := normalizedReleaseFilename(c.rel.Name, c.rel.Info.Group); fn != "" {
			fnKey = c.rel.Protocol + "|" + repostSeasonEpisodeScope(c.rel) + "|" + fn
		}
		attrKey := ""
		if sig := parsedRepostSignature(c.rel); sig != "" {
			attrKey = c.rel.Protocol + "|" + sig
		}
		if idx := findLeader(c, fnKey, attrKey, cid, bucket); idx >= 0 {
			leaders[idx].fallbacks = append(leaders[idx].fallbacks, c)
			if len(c.fallbacks) > 0 {
				leaders[idx].fallbacks = append(leaders[idx].fallbacks, c.fallbacks...)
				c.fallbacks = nil
			}
			continue
		}
		idx := len(leaders)
		leaders = append(leaders, c)
		if cid != "" {
			byContent[cid] = append(byContent[cid], idx)
		}
		if fnKey != "" {
			bk := bucketKey{key: fnKey, bucket: bucket}
			byFile[bk] = append(byFile[bk], idx)
		}
		if attrKey != "" {
			bk := bucketKey{key: attrKey, bucket: bucket}
			byAttr[bk] = append(byAttr[bk], idx)
		}
	}
	return leaders
}

func countGroupedMembers(cands []candidate) int {
	n := 0
	for _, c := range cands {
		n += 1 + len(c.fallbacks)
	}
	return n
}
