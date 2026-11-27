package debrid

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// packUnit is one episode-sized thing in a Usenet season pack: a plain
// video file or one archive set. Names are what the episode is read from.
type packUnit struct {
	name    string
	bytes   int64
	files   map[*nzb.File]bool
	archive bool // an archive set, whose members' names the NZB does not show
}

// PackNamer writes real file names (read from PAR2 or the first articles)
// into an obfuscated NZB's subjects. Implemented by *stream.Engine.
type PackNamer interface {
	NameFiles(ctx context.Context, releaseID string, nzbData []byte) ([]byte, error)
}

// minUnnamedBytes is the size below which an unidentified file (an nfo, a
// sample part) is not worth probing.
const minUnnamedBytes = 1 << 20

// episodeNZB is EpisodeNZB for a release whose files may be obfuscated:
// unidentifiable big files are named first when a namer is set.
func (x *Indexer) episodeNZB(ctx context.Context, r core.Release, data []byte) ([]byte, error) {
	if x.packNamer != nil && hasUnnamedFiles(data) {
		named, err := x.packNamer.NameFiles(ctx, r.ID, data)
		if err != nil {
			return nil, err
		}
		data = named
	}
	return EpisodeNZB(data, r.Target)
}

func hasUnnamedFiles(data []byte) bool {
	doc, err := nzb.Parse(data)
	if err != nil {
		return false
	}
	for _, f := range doc.Files {
		if f.Info.Kind == nzb.KindUnknown && f.Bytes() >= minUnnamedBytes {
			return true
		}
	}
	return false
}

// NeedsExactFileNaming reports whether an explicit ExactFile selection failed
// against an NZB that still has obfuscated or unidentified files whose real
// PAR2/yEnc names have not been probed yet.
func NeedsExactFileNaming(data []byte, t *core.ReleaseTarget) bool {
	if t == nil || t.ExactFile == "" {
		return false
	}
	doc, err := nzb.Parse(data)
	if err != nil {
		return false
	}
	if t.FileIndex != nil {
		i := *t.FileIndex
		if i < 0 || i >= len(doc.Files) {
			return false
		}
		f := doc.Files[i]
		return f.Obfuscated || f.Info.Kind == nzb.KindUnknown
	}
	for _, f := range doc.Files {
		if f.Obfuscated || f.Info.Kind == nzb.KindUnknown {
			return true
		}
	}
	return false
}

func coreTrackToNZB(t *core.TrackHint) *nzb.TrackHint {
	if t == nil || (t.Number <= 0 && strings.TrimSpace(t.Title) == "") {
		return nil
	}
	return &nzb.TrackHint{
		Disc:   t.Disc,
		Number: t.Number,
		Title:  strings.TrimSpace(t.Title),
	}
}

// EpisodeNZB narrows the NZB of a Usenet season pack to the files of the
// wanted episode (its video, or its archive set, plus its PAR2 index).
// The stream engine then plays that episode like any single-episode
// release. A pack with no file for the episode wraps core.ErrNotAvailable.
func EpisodeNZB(data []byte, t *core.ReleaseTarget) ([]byte, error) {
	if t != nil && t.AllowNonVideo && (t.FileIndex != nil || t.ExactFile != "") {
		return exactFileNZB(data, t)
	}
	if t != nil && t.AllowNonVideo && (t.Member != "" || t.MemberIndex != nil || coreTrackToNZB(t.Track) != nil || nzb.IsNonVideoMIME(t.MIME)) {
		return nonVideoNZB(data, t)
	}
	if t != nil && t.Episode <= 0 && (len(t.MovieTitles) > 0 || len(t.WantedLanguages) > 0) {
		doc, err := nzb.Parse(data)
		if err != nil {
			return nil, err
		}
		hint := nzb.MovieHint{
			Titles:           t.MovieTitles,
			Year:             t.MovieYear,
			WantedLanguages:  t.WantedLanguages,
			OriginalLanguage: t.OriginalLanguage,
		}
		out, ok := doc.SubsetMeta(func(f *nzb.File) bool {
			return true // the engine arbitrates after learning the complete playable file list
		}, map[string]string{nzb.MetaMovie: hint.String()})
		if !ok {
			return nil, core.ErrWrongContent
		}
		return out, nil
	}
	if t == nil || t.Episode <= 0 {
		return data, nil
	}
	doc, err := nzb.Parse(data)
	if err != nil {
		return nil, err
	}
	units := packUnits(doc)
	var chosen []packUnit
	var extra map[string]string
	if u := pickPackUnit(units, t); u != nil {
		chosen = []packUnit{*u}
	} else if opaque := opaqueArchives(units, t); len(opaque) > 0 {
		// No file name carries the episode, but an archive set whose name
		// says nothing may hold the season. Keep it whole and tell the stream
		// engine which member to play; it rejects the release if none matches.
		chosen = opaque
		hint := nzb.EpisodeHint{Season: t.Season, Episode: t.Episode, Absolute: t.AbsoluteEpisode}
		for _, n := range t.Aliases {
			if n.Season > 0 && n.Episode > 0 {
				hint.Aliases = append(hint.Aliases, [2]int{n.Season, n.Episode})
			}
		}
		extra = map[string]string{nzb.MetaEpisode: hint.String()}
	}
	if len(chosen) == 0 {
		return nil, fmt.Errorf("usenet season pack has no file for S%02dE%02d: %w", t.Season, t.Episode, core.ErrNotAvailable)
	}
	keep := map[*nzb.File]bool{}
	var stems []string
	for _, u := range chosen {
		stems = append(stems, strings.ToLower(u.name))
		for f := range u.files {
			keep[f] = true
		}
	}
	// PAR2 files named after the episode, and the pack's index when the
	// episode has none of its own. The index also lists the other episodes;
	// the engine's integrity check looks up the stream's file by name and
	// never consults those entries. Recovery volumes are never kept: they
	// span the whole pack and nothing here repairs.
	own := map[*nzb.File]bool{}
	ownIndex := false
	for _, f := range doc.Files {
		if f.Info.Kind != nzb.KindPar2 || nzb.IsPar2Volume(f.Name) {
			continue
		}
		for _, stem := range stems {
			if strings.HasPrefix(strings.ToLower(f.Name), stem) {
				own[f] = true
				ownIndex = true
			}
		}
	}
	packIndex := nzb.Par2Index(doc.Files)
	out, ok := doc.SubsetMeta(func(f *nzb.File) bool {
		return keep[f] || own[f] || (!ownIndex && f == packIndex)
	}, extra)
	if !ok {
		return nil, fmt.Errorf("usenet season pack has no file for S%02dE%02d: %w", t.Season, t.Episode, core.ErrNotAvailable)
	}
	return out, nil
}

// exactFileNZB narrows an NZB to one explicitly selected file — by NZB
// document index (FileIndex, the order a release_files listing returns) or
// by name (ExactFile, full name or unique basename) — plus its archive set
// when the file is one volume of one, and its PAR2 index. This is how a
// plugin's file choice is honoured on Usenet as ChooseFile honours it on
// debrid.
func exactFileNZB(data []byte, t *core.ReleaseTarget) ([]byte, error) {
	doc, err := nzb.Parse(data)
	if err != nil {
		return nil, err
	}
	var selected *nzb.File
	if t.FileIndex != nil {
		i := *t.FileIndex
		if i < 0 || i >= len(doc.Files) {
			return nil, fmt.Errorf("usenet fileIndex %d outside file list of %d: %w", i, len(doc.Files), core.ErrWrongContent)
		}
		selected = doc.Files[i]
		if t.ExactFile != "" && !exactNZBFileMatches(selected, t.ExactFile) {
			return nil, wrongContentIndexError(i, selected, t.ExactFile)
		}
	} else {
		var matches []*nzb.File
		for _, f := range doc.Files {
			if exactNZBFileMatches(f, t.ExactFile) {
				matches = append(matches, f)
			}
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("usenet file %q matched %d files: %w", t.ExactFile, len(matches), core.ErrWrongContent)
		}
		selected = matches[0]
	}

	// Serve loose posted files as raw bytes. For archive files, keep and
	// unwrap the archive set unless the caller explicitly asked for a raw
	// wrapper-archive MIME with no member selector.
	unwrapArchive := selected.Info.Kind.IsArchive() &&
		(!nzb.IsArchiveMIME(t.MIME) || t.Member != "" || t.MemberIndex != nil || coreTrackToNZB(t.Track) != nil)
	rawFile := !unwrapArchive

	keep := map[*nzb.File]bool{selected: true}
	stems := []string{strings.ToLower(strings.TrimSuffix(selected.Name, path.Ext(selected.Name)))}
	if orig := selected.OriginalName(); orig != "" && orig != selected.Name {
		if stem := strings.ToLower(strings.TrimSuffix(orig, path.Ext(orig))); stem != "" && stem != stems[0] {
			stems = append(stems, stem)
		}
	}
	if unwrapArchive {
		for _, s := range nzb.GroupSets(doc.Files) {
			for _, v := range s.Volumes {
				if v == selected {
					for _, vol := range s.Volumes {
						keep[vol] = true
					}
					stems = append(stems, strings.ToLower(s.Name))
				}
			}
		}
	}
	own := map[*nzb.File]bool{}
	ownIndex := false
	for _, f := range doc.Files {
		if f.Info.Kind != nzb.KindPar2 || nzb.IsPar2Volume(f.Name) {
			continue
		}
		for _, stem := range stems {
			if strings.HasPrefix(strings.ToLower(f.Name), stem) {
				own[f] = true
				ownIndex = true
			}
		}
	}
	packIndex := nzb.Par2Index(doc.Files)
	hint := nzb.NonVideoHint{
		ExplicitFile: true,
		ExactFile:    selected.Name,
		RawFile:      rawFile,
		Member:       strings.TrimSpace(t.Member),
		MemberIndex:  t.MemberIndex,
		Track:        coreTrackToNZB(t.Track),
		MIME:         nzb.NormalizeMIME(t.MIME),
	}
	extra := map[string]string{nzb.MetaNonVideo: hint.String()}
	if pw := strings.TrimSpace(t.ArchivePassword); pw != "" && doc.Password() == "" {
		extra["password"] = pw
	}
	out, ok := doc.SubsetMeta(func(f *nzb.File) bool {
		return keep[f] || own[f] || (!ownIndex && f == packIndex)
	}, extra)
	if !ok {
		return nil, fmt.Errorf("usenet release has no file %q: %w", t.ExactFile, core.ErrWrongContent)
	}
	return out, nil
}

// nonVideoNZB annotates an NZB with ether-nonvideo metadata so the stream
// engine selects the requested non-video member or media-MIME file (loose or
// archived) instead of running video-only heuristics.
func nonVideoNZB(data []byte, t *core.ReleaseTarget) ([]byte, error) {
	doc, err := nzb.Parse(data)
	if err != nil {
		return nil, err
	}
	hint := nzb.NonVideoHint{
		Member:      strings.TrimSpace(t.Member),
		MemberIndex: t.MemberIndex,
		Track:       coreTrackToNZB(t.Track),
		MIME:        nzb.NormalizeMIME(t.MIME),
	}
	extra := map[string]string{nzb.MetaNonVideo: hint.String()}
	if pw := strings.TrimSpace(t.ArchivePassword); pw != "" && doc.Password() == "" {
		extra["password"] = pw
	}
	out, ok := doc.SubsetMeta(func(f *nzb.File) bool { return true }, extra)
	if !ok {
		return nil, fmt.Errorf("usenet release has no files: %w", core.ErrNoUsableFile)
	}
	return out, nil
}

func exactNZBFileMatches(f *nzb.File, want string) bool {
	if f == nil {
		return false
	}
	if exactNZBNameMatches(f.Name, want) {
		return true
	}
	if orig := f.OriginalName(); orig != "" && orig != f.Name && exactNZBNameMatches(orig, want) {
		return true
	}
	return false
}

func wrongContentIndexError(i int, f *nzb.File, want string) error {
	orig := f.OriginalName()
	if orig != "" && orig != f.Name {
		return fmt.Errorf("usenet file at index %d is %q (subject %q), not %q: %w", i, f.Name, orig, want, core.ErrWrongContent)
	}
	actual := f.Name
	if actual == "" {
		actual = orig
	}
	return fmt.Errorf("usenet file at index %d is %q, not %q: %w", i, actual, want, core.ErrWrongContent)
}

func exactNZBNameMatches(name, want string) bool {
	want = strings.ReplaceAll(strings.TrimSpace(want), "\\", "/")
	name = strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	if name == "" || want == "" {
		return false
	}
	if strings.TrimPrefix(name, "/") == strings.TrimPrefix(want, "/") {
		return true
	}
	// NZB file names carry no directories, so a torrent-style path the
	// plugin learned elsewhere still selects by its basename.
	return path.Base(name) == path.Base(want)
}

// opaqueArchives returns the archive sets whose names carry no episode (an
// obfuscated base name), the ones that may hold a whole season. A set whose
// name does give a season or cour must be the target's: "Show.S02.rar" of a
// complete-series pack never stands in for season 1, whose classic-numbered
// "05 - Title.mkv" member would otherwise match S01E05.
func opaqueArchives(units []packUnit, t *core.ReleaseTarget) []packUnit {
	var out []packUnit
	for _, u := range units {
		info := releaseparse.Parse(u.name)
		if !u.archive || len(info.Episodes) > 0 || len(info.AbsoluteEpisodes) > 0 {
			continue
		}
		if len(info.Seasons) > 0 && !targetAllowsSeason(t, info.Seasons) || !fileCourMatchesTarget(u.name+".mkv", t) {
			continue
		}
		out = append(out, u)
	}
	return out
}

func packUnits(doc *nzb.NZB) []packUnit {
	var units []packUnit
	var vols []*nzb.File
	for _, f := range doc.Files {
		switch {
		case f.Info.Kind == nzb.KindVideo && !f.Info.Sample && !f.Info.Extra:
			name := strings.TrimSuffix(f.Name, path.Ext(f.Name))
			units = append(units, packUnit{name: name, bytes: f.Bytes(), files: map[*nzb.File]bool{f: true}})
		case f.Info.Kind.IsArchive():
			vols = append(vols, f)
		}
	}
	for _, s := range nzb.GroupSets(vols) {
		if len(s.Volumes) == 0 || s.Volumes[0].Info.Sample {
			continue
		}
		u := packUnit{name: s.Name, bytes: s.Bytes(), files: map[*nzb.File]bool{}, archive: true}
		for _, v := range s.Volumes {
			u.files[v] = true
		}
		units = append(units, u)
	}
	return units
}

// pickPackUnit returns the largest unit whose name carries the target
// episode within plausible pack episode size bounds, or nil. The release
// itself already matched the series, so the check is the episode number alone.
func pickPackUnit(units []packUnit, t *core.ReleaseTarget) *packUnit {
	sized := make([]releaseparse.PackSizedFile, 0, len(units))
	for _, u := range units {
		if fileCourMatchesTarget(u.name+".mkv", t) {
			sized = append(sized, releaseparse.PackSizedFile{Path: u.name + ".mkv", Size: u.bytes})
		}
	}
	sizeGate := releaseparse.NewPackEpisodeSizeGate(sized, t.Season, t.Cour)

	var best, seasonCounted *packUnit
	for i := range units {
		u := &units[i]
		if !fileCourMatchesTarget(u.name+".mkv", t) || !sizeGate.Accept(u.name+".mkv", u.bytes) {
			continue
		}
		info := releaseparse.Parse(u.name)
		ok := releaseparse.EpisodeMatches(info, t.Season, t.Episode) ||
			(t.AbsoluteEpisode > 0 && releaseparse.EpisodeMatchesAbsolute(info, t.AbsoluteEpisode)) ||
			(t.Season > 0 && len(info.Episodes) == 0 && len(info.AbsoluteEpisodes) == 0 && releaseparse.EpisodeMatchesAirDateString(info, t.AirDate)) ||
			(len(info.Episodes) == 0 && classicPackEpisodeMatches(u.name+".mkv", t.Season, t.Episode))
		if !ok && info.Cour == 0 {
			for _, n := range t.Aliases {
				if releaseparse.EpisodeMatches(info, n.Season, n.Episode) {
					ok = true
					break
				}
			}
		}
		if info.Cour > 0 && t.Cour == info.Cour && t.CourEpisode > 0 {
			if releaseparse.EpisodeMatches(info, t.Season, t.CourEpisode) ||
				releaseparse.EpisodeMatchesAbsolute(info, t.CourEpisode) {
				if best == nil || u.bytes > best.bytes {
					best = u
				}
				continue
			}
			// A unit numbered within the season ("S04.Part.2.S04E17")
			// stands in only when none carries the cour-relative number.
			if ok && !info.CourRelative && releaseparse.EpisodeMatches(info, t.Season, t.Episode) &&
				(seasonCounted == nil || u.bytes > seasonCounted.bytes) {
				seasonCounted = u
			}
			continue
		}
		if ok && (best == nil || u.bytes > best.bytes) {
			best = u
		}
	}
	if best == nil {
		return seasonCounted
	}
	return best
}
