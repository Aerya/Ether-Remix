package releaseparse

import (
	"path"
	"slices"
	"sort"
	"strings"
)

const movieFullLengthMinPercent = 35

// MovieFileMatches requires an exact parsed title, never a prefix that can
// admit sequels. A year carried by the file or its nearest titled folder must
// agree too. Trailing language/audio/subtitle labels (e.g. "The.Mummy.FRENCH",
// "The Mummy (English)") and generic language-only filenames ("French/movie.mkv")
// inside single-movie dual-language packs are accepted when no folder contradicts
// the movie's title or year.
func MovieFileMatches(name string, titles []string, year int) bool {
	name = strings.ReplaceAll(name, `\`, "/")
	base := path.Base(name)
	stem := strings.TrimSuffix(base, path.Ext(base))
	info := Parse(stem)
	matches := func(title string) bool {
		for _, want := range titles {
			if want != "" && NormalizeTitle(title) == NormalizeTitle(want) {
				return true
			}
		}
		return false
	}
	titleMatched := matchesTitleOrStripped(info.Title, matches)
	if !titleMatched && isGenericMovieAudioStem(stem) && (year == 0 || info.Year == 0 || info.Year == year) {
		// Dual-language packs sometimes place "French/movie.mkv" or "English.mkv"
		// inside a release already matched to the movie. Admit it provided no
		// enclosing folder names a different movie or conflicting year.
		for dir := path.Dir(name); dir != "." && dir != "/"; dir = path.Dir(dir) {
			dirBase := path.Base(dir)
			if isGenericMovieAudioStem(dirBase) {
				continue
			}
			parent := Parse(dirBase)
			if !matchesTitleOrStripped(parent.Title, matches) {
				return false
			}
			if year != 0 && parent.Year != 0 && parent.Year != year {
				return false
			}
			return true
		}
		return true
	}
	if !titleMatched {
		return false
	}
	if info.Year == 0 {
		for dir := path.Dir(name); dir != "." && dir != "/"; dir = path.Dir(dir) {
			parent := Parse(path.Base(dir))
			if matchesTitleOrStripped(parent.Title, matches) && parent.Year != 0 {
				info.Year = parent.Year
				break
			}
		}
	}
	return year == 0 || info.Year == 0 || info.Year == year
}

func matchesTitleOrStripped(title string, matches func(string) bool) bool {
	cur := strings.TrimSpace(title)
	if matches(cur) {
		return true
	}
	for range 4 {
		next, ok := stripTrailingMovieAudioWord(cur)
		if !ok {
			break
		}
		cur = next
		if matches(cur) {
			return true
		}
	}
	return false
}

func stripTrailingMovieAudioWord(title string) (string, bool) {
	title = strings.TrimSpace(title)
	if stripped, ok := StripTrailingLanguage(title); ok {
		return stripped, true
	}
	idx := strings.LastIndexByte(title, ' ')
	if idx <= 0 {
		return title, false
	}
	tail := strings.TrimSpace(title[idx+1:])
	rest := strings.TrimSpace(title[:idx])
	if rest == "" {
		return title, false
	}
	for _, st := range subtitleTags {
		if strings.EqualFold(tail, st.token) {
			return rest, true
		}
	}
	switch strings.ToUpper(tail) {
	case "MULTI", "DUAL", "AUDIO", "DUB", "DUBBED", "DUBLADO", "DUBLADA", "DUBLADOS", "DUBLADAS", "SUB", "SUBBED", "VO", "OV", "ORIG", "ORIGINAL", "DL", "ML":
		return rest, true
	}
	return title, false
}

func isGenericMovieAudioStem(stem string) bool {
	cur := strings.TrimSpace(spacify(stem))
	if cur == "" {
		return false
	}
	for range 6 {
		if KnownLanguage(cur) {
			return true
		}
		switch strings.ToUpper(cur) {
		case "MOVIE", "FILM", "VIDEO", "FEATURE", "MAIN", "MULTI", "DUAL", "DUB", "DUBBED", "VO", "OV", "ORIGINAL":
			return true
		}
		for _, st := range subtitleTags {
			if strings.EqualFold(cur, st.token) {
				return true
			}
		}
		next, ok := stripTrailingMovieAudioWord(cur)
		if !ok {
			return false
		}
		cur = next
	}
	return false
}

// DropTitleLanguages removes languages named only by the title's own words
// ("The French Dispatch", "The German Doctor") when the normalized title
// occurs inside name.
func DropTitleLanguages(langs []string, name, title string) []string {
	titleLangs := Parse(title).Languages
	if len(titleLangs) == 0 {
		return langs
	}
	nName, nTitle := NormalizeTitle(name), NormalizeTitle(title)
	i := strings.Index(nName, nTitle)
	if nTitle == "" || i < 0 {
		return langs
	}
	rest := Parse(nName[:i] + " " + nName[i+len(nTitle):]).Languages
	var out []string
	for _, l := range langs {
		if slices.Contains(titleLangs, l) && !slices.Contains(rest, l) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// MovieCandidate describes one playable movie file or archive set for
// language-aware multi-file pack ordering.
type MovieCandidate struct {
	Path string
	Size int64
}

type movieLanguageRank struct {
	matched         bool
	wantedRank      int // len(wanted) - bestIdx; higher means earlier in wanted
	matchKind       int // 3 = exact, 2 = multi/dual-audio, 1 = regional base match
	untaggedNeutral int // 1 when untagged and originalLang is unknown; 0 otherwise
}

func (a movieLanguageRank) betterThan(b movieLanguageRank) bool {
	if a.matched != b.matched {
		return a.matched
	}
	if a.wantedRank != b.wantedRank {
		return a.wantedRank > b.wantedRank
	}
	if a.matchKind != b.matchKind {
		return a.matchKind > b.matchKind
	}
	return a.untaggedNeutral > b.untaggedNeutral
}

// OrderMovieCandidates returns a permutation of [0..len(cands)-1] preferring
// full-length, top-resolution candidates whose parsed audio matches wanted (in
// priority order) before falling back to largest byte size.
func OrderMovieCandidates(cands []MovieCandidate, titles []string, originalLang string, wanted []string) []int {
	idx := make([]int, len(cands))
	for i := range idx {
		idx[i] = i
	}
	if len(cands) <= 1 {
		return idx
	}
	var maxSize int64
	for _, c := range cands {
		if c.Size > maxSize {
			maxSize = c.Size
		}
	}
	var normWanted []string
	for _, w := range wanted {
		if nw := LanguageName(w); nw != "" && !slices.Contains(normWanted, nw) {
			normWanted = append(normWanted, nw)
		}
	}
	if len(normWanted) == 0 || maxSize <= 0 {
		sort.SliceStable(idx, func(a, b int) bool { return cands[idx[a]].Size > cands[idx[b]].Size })
		return idx
	}

	commonDir := commonMovieDir(cands)
	fullLength := make([]bool, len(cands))
	heights := make([]int, len(cands))
	scores := make([]movieLanguageRank, len(cands))
	maxFullHeight := 0

	for i, c := range cands {
		relPath := trimMovieCommonDir(c.Path, commonDir)
		fullLength[i] = c.Size*100 >= movieFullLengthMinPercent*maxSize
		heights[i] = Parse(strings.ReplaceAll(relPath, "/", " ")).Height
		if fullLength[i] && heights[i] > maxFullHeight {
			maxFullHeight = heights[i]
		}
		scores[i] = scoreMovieCandidateLanguage(relPath, titles, originalLang, normWanted)
	}

	eligible := func(i int) bool {
		return fullLength[i] && (heights[i] == 0 || maxFullHeight == 0 || heights[i] >= maxFullHeight)
	}

	sort.SliceStable(idx, func(a, b int) bool {
		ia, ib := idx[a], idx[b]
		ea, eb := eligible(ia), eligible(ib)
		if ea != eb {
			return ea
		}
		if ea && scores[ia] != scores[ib] {
			return scores[ia].betterThan(scores[ib])
		}
		return cands[ia].Size > cands[ib].Size
	})
	return idx
}

func scoreMovieCandidateLanguage(relPath string, titles []string, originalLang string, wanted []string) movieLanguageRank {
	base := path.Base(relPath)
	inspect := strings.TrimSuffix(base, path.Ext(base))
	if dir := path.Dir(relPath); dir != "." && dir != "/" && dir != "" {
		inspect = inspect + " " + strings.ReplaceAll(dir, "/", " ")
	}
	info := Parse(inspect)

	var langs []string
	hasMulti := false
	for _, l := range info.Languages {
		switch l {
		case "":
		case LanguageMulti:
			hasMulti = true
		case LanguageDual:
		default:
			if !slices.Contains(langs, l) {
				langs = append(langs, l)
			}
		}
	}
	for _, fl := range FlagLanguages(inspect) {
		if !slices.Contains(langs, fl) {
			langs = append(langs, fl)
		}
	}

	orig := LanguageName(originalLang)
	if len(langs) == 1 && langs[0] == "german" {
		if hasGermanDualToken(inspect) {
			if orig != "" && !slices.Contains(langs, orig) {
				langs = append(langs, orig)
			}
		} else if hasGermanMultiToken(inspect) {
			if orig != "" && !slices.Contains(langs, orig) {
				langs = append(langs, orig)
			}
			if !slices.Contains(langs, "english") {
				langs = append(langs, "english")
			}
		}
	}

	for _, t := range titles {
		if t != "" {
			langs = DropTitleLanguages(langs, inspect, t)
		}
	}

	explicit := len(langs) > 0 || hasMulti
	if !explicit && !info.Dubbed && orig != "" {
		langs = []string{orig}
	}

	var best movieLanguageRank
	for wi, want := range wanted {
		rank := len(wanted) - wi
		for _, l := range langs {
			m := LanguageMatch(l, want)
			if m == LanguageNoMatch {
				continue
			}
			kind := 1
			if m == LanguageExactMatch {
				kind = 3
			}
			cand := movieLanguageRank{matched: true, wantedRank: rank, matchKind: kind}
			if cand.betterThan(best) {
				best = cand
			}
		}
	}
	if hasMulti {
		cand := movieLanguageRank{matched: true, wantedRank: len(wanted), matchKind: 2}
		if cand.betterThan(best) {
			best = cand
		}
	}
	if !best.matched && !explicit && !info.Dubbed && orig == "" {
		best.untaggedNeutral = 1
	}
	return best
}

func hasGermanDualToken(s string) bool {
	fields := strings.Fields(spacify(s))
	for i, f := range fields {
		if strings.EqualFold(f, "DL") && (i == 0 || !strings.EqualFold(fields[i-1], "WEB")) {
			return true
		}
	}
	return false
}

func hasGermanMultiToken(s string) bool {
	return tokenIndex(spacify(s), "ML") >= 0
}

func commonMovieDir(cands []MovieCandidate) string {
	if len(cands) < 2 {
		return ""
	}
	splitDir := func(p string) []string {
		d := path.Dir(strings.ReplaceAll(p, `\`, "/"))
		if d == "." || d == "/" || d == "" {
			return nil
		}
		return strings.Split(strings.Trim(d, "/"), "/")
	}
	common := splitDir(cands[0].Path)
	for _, c := range cands[1:] {
		parts := splitDir(c.Path)
		n := min(len(common), len(parts))
		common = common[:n]
		for i := range n {
			if !strings.EqualFold(common[i], parts[i]) {
				common = common[:i]
				break
			}
		}
		if len(common) == 0 {
			return ""
		}
	}
	return strings.Join(common, "/")
}

func trimMovieCommonDir(p, commonDir string) string {
	norm := strings.TrimLeft(strings.ReplaceAll(p, `\`, "/"), "/")
	if commonDir == "" {
		return norm
	}
	if len(norm) > len(commonDir) && strings.EqualFold(norm[:len(commonDir)], commonDir) && norm[len(commonDir)] == '/' {
		return norm[len(commonDir)+1:]
	}
	return norm
}
