package stream

import (
	"context"
	"errors"
	"fmt"

	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
)

// MaxArticleCheckSamples is the hard ceiling on articles sampled by one
// CheckArticles call, regardless of how many files or segments an NZB carries.
const MaxArticleCheckSamples = 32

type articleCheckSet struct {
	kind  nzb.Kind
	files []*postedFile
}

// CheckArticles runs Ether's sampled, bounded STAT/BODY article availability
// check against an NZB and reports {Available, Checked, Missing}.
//
// For RAR (and split-RAR) releases it also inspects the archive header using
// the open path's parsers (reordering out-of-order volumes before rejecting),
// failing fast on compressed video members, missing first volumes, or
// unorderable parts.
//
// Every NNTP operation runs at background (prefetch) priority through the
// shared provider pool and connection limits, so plugin pre-checks never
// starve live playback. Transient provider/network errors return an error
// instead of a negative verdict so callers do not cache transient outages as
// missing articles.
func (e *Engine) CheckArticles(ctx context.Context, releaseID string, nzbData []byte) (core.UsenetCheckResult, error) {
	if err := e.ctx.Err(); err != nil {
		return core.UsenetCheckResult{}, ErrClosed
	}
	if e.fetcher == nil {
		return core.UsenetCheckResult{}, fmt.Errorf("stream: usenet check unavailable: %w", core.ErrSourceUnavailable)
	}
	// Always demote plugin article checks to prefetch priority so provider
	// connection limits, reserveLive, and priority queueing protect playback.
	if !core.IsBackgroundCheck(ctx) {
		ctx = core.WithBackgroundBudget(ctx, func(context.Context, string, string, int64) error { return nil })
	}
	ctx = context.WithValue(ctx, healthWorkKey{}, true)

	rel, err := e.acquire(nzbData)
	if err != nil {
		return core.UsenetCheckResult{}, err
	}
	defer e.release(rel)

	files, archSets, missingVols := e.selectArticleCheckFiles(ctx, rel)
	if missingVols > 0 {
		capped := min(missingVols, MaxArticleCheckSamples)
		return core.UsenetCheckResult{
			Available: false,
			Checked:   capped,
			Missing:   capped,
			Reason:    "volumes_missing",
		}, nil
	}
	if len(files) == 0 {
		return core.UsenetCheckResult{
			Available: false,
			Checked:   0,
			Missing:   0,
			Reason:    "no_articles",
		}, nil
	}

	// Structurally incomplete NZBs (gaps in XML segment numbering) are already
	// known incomplete without spending any NNTP round trips, matching planFor.
	xmlMissing := 0
	for _, pf := range files {
		xmlMissing += len(pf.file.Missing())
	}
	if xmlMissing > 0 {
		capped := min(xmlMissing, MaxArticleCheckSamples)
		return core.UsenetCheckResult{
			Available: false,
			Checked:   capped,
			Missing:   capped,
			Reason:    "segment_absent_from_nzb",
		}, nil
	}

	headerCalls, reorderedFiles, verdict, herr := e.checkArchiveHeaders(ctx, rel, releaseID, files, archSets)
	if herr != nil {
		return core.UsenetCheckResult{}, herr
	}
	if verdict != nil {
		return *verdict, nil
	}
	if len(reorderedFiles) > 0 {
		files = reorderedFiles
	}

	remaining := MaxArticleCheckSamples - headerCalls
	if remaining <= 0 {
		return core.UsenetCheckResult{
			Available: true,
			Checked:   min(headerCalls, MaxArticleCheckSamples),
			Missing:   0,
		}, nil
	}

	out := e.statSampleBounded(ctx, files, remaining)
	if out.missing > 0 {
		reason := "articles_missing"
		if out.missingFromNZB {
			reason = "segment_absent_from_nzb"
		}
		checked := max(min(headerCalls+out.checked, MaxArticleCheckSamples), out.missing)
		return core.UsenetCheckResult{
			Available: false,
			Checked:   checked,
			Missing:   out.missing,
			Reason:    reason,
		}, nil
	}
	if out.err != nil {
		return core.UsenetCheckResult{}, out.err
	}
	if out.total == 0 {
		return core.UsenetCheckResult{
			Available: false,
			Checked:   0,
			Missing:   0,
			Reason:    "no_articles",
		}, nil
	}
	return core.UsenetCheckResult{
		Available: true,
		Checked:   min(headerCalls+out.checked, MaxArticleCheckSamples),
		Missing:   0,
	}, nil
}

// checkArchiveHeaders reads the leading archive volume header(s) for RAR and
// split-RAR releases using the open path's parsers and reordering logic. It
// returns a non-nil verdict when the archive is permanently unstreamable or
// missing articles, or a non-nil error for transient provider failures.
func (e *Engine) checkArchiveHeaders(ctx context.Context, rel *release, releaseID string, files []*postedFile, sets []articleCheckSet) (int, []*postedFile, *core.UsenetCheckResult, error) {
	b := &builder{
		e:    e,
		rel:  rel,
		prio: healthPriority(ctx),
		fo:   fetchOpts{cacheID: releaseID, layoutProbe: true, noRepair: true},
	}

	if len(sets) > 0 {
		checkSets := sets
		if len(checkSets) > 3 {
			checkSets = checkSets[:1]
		}
		headerCalls := 0
		var reorderedFiles []*postedFile
		for _, s := range checkSets {
			budget := MaxArticleCheckSamples - headerCalls
			if budget <= 0 {
				break
			}
			ord, err := b.orderRARVolumes(ctx, s.files, orderRAROpts{
				splitSet:             s.kind == nzb.KindSplit,
				maxFetches:           budget,
				checkVideoCompressed: true,
			})
			headerCalls += ord.fetches
			if err != nil {
				switch {
				case errors.Is(err, errArchiveFirstVolumeMissing):
					v := core.UsenetCheckResult{Available: false, Checked: max(headerCalls, 1), Missing: 0, Reason: "archive_first_volume_missing"}
					return headerCalls, nil, &v, nil
				case errors.Is(err, errArchivePartsOutOfOrder):
					v := core.UsenetCheckResult{Available: false, Checked: max(headerCalls, 1), Missing: 0, Reason: "archive_parts_out_of_order"}
					return headerCalls, nil, &v, nil
				case errors.Is(err, archive.ErrMissingVolume):
					v := core.UsenetCheckResult{Available: false, Checked: max(headerCalls, 1), Missing: 1, Reason: "volumes_missing"}
					return headerCalls, nil, &v, nil
				case errors.Is(err, archive.ErrCompressed), errors.Is(err, archive.ErrSolid):
					v := core.UsenetCheckResult{Available: false, Checked: max(headerCalls, 1), Missing: 0, Reason: "archive_compressed"}
					return headerCalls, nil, &v, nil
				case isMissing(err):
					v := core.UsenetCheckResult{Available: false, Checked: max(headerCalls, 1), Missing: 1, Reason: "articles_missing"}
					return headerCalls, nil, &v, nil
				case errors.Is(err, archive.ErrEncrypted), errors.Is(err, archive.ErrPasswordRequired):
					// Header-encrypted RAR without password in the NZB: a
					// plugin may supply ArchivePassword at open time.
					continue
				default:
					return headerCalls, nil, nil, err
				}
			}
			if reason := checkRARVolumeForCheck(ord.v0); reason != "" {
				v := core.UsenetCheckResult{Available: false, Checked: max(headerCalls, 1), Missing: 0, Reason: reason}
				return headerCalls, nil, &v, nil
			}
			if reason := checkRARVolumeForCheck(ord.v1); reason != "" {
				v := core.UsenetCheckResult{Available: false, Checked: max(headerCalls, 1), Missing: 0, Reason: reason}
				return headerCalls, nil, &v, nil
			}
			if ord.reordered && len(checkSets) == 1 && len(files) == len(ord.vols) {
				reorderedFiles = ord.vols
			}
		}
		return headerCalls, reorderedFiles, nil, nil
	}

	// Obfuscated NZBs without archive extensions: sniff the largest unknown
	// file's first article (1 fetch) so compressed obfuscated RARs are caught
	// at check time without false positives on non-RAR releases.
	var largestUnknown *postedFile
	for _, pf := range files {
		rel.infoMu.RLock()
		kind := pf.file.Info.Kind
		rel.infoMu.RUnlock()
		if kind == nzb.KindUnknown && (largestUnknown == nil || pf.file.Bytes() > largestUnknown.file.Bytes()) {
			largestUnknown = pf
		}
	}
	if largestUnknown == nil {
		return 0, nil, nil, nil
	}
	seg, err := e.segment(ctx, rel, largestUnknown, 0, b.prio, b.fo)
	if err != nil {
		if isMissing(err) {
			v := core.UsenetCheckResult{Available: false, Checked: 1, Missing: 1, Reason: "articles_missing"}
			return 1, nil, &v, nil
		}
		return 1, nil, nil, err
	}
	if seg.begin != 0 && len(files) == 1 {
		v := core.UsenetCheckResult{Available: false, Checked: 1, Missing: 0, Reason: "archive_parts_out_of_order"}
		return 1, nil, &v, nil
	}
	if seg.begin == 0 && isRARFormat(archive.Detect(seg.data)) {
		if v0, verr := b.readCachedRARVolume(ctx, largestUnknown, seg.data); verr == nil {
			if reason := checkRARVolumeForCheck(v0); reason != "" {
				v := core.UsenetCheckResult{Available: false, Checked: 1, Missing: 0, Reason: reason}
				return 1, nil, &v, nil
			}
			if len(files) == 1 && rarVolumeIsContinuation(v0) {
				v := core.UsenetCheckResult{Available: false, Checked: 1, Missing: 0, Reason: "archive_first_volume_missing"}
				return 1, nil, &v, nil
			}
		}
	}
	return 1, nil, nil, nil
}

// selectArticleCheckFiles chooses the payload files of an NZB to sample,
// returns any RAR/split archive sets to header-check, and reports any archive
// volumes missing from the selected archive set(s). For video/archive releases
// it mirrors CheckRelease's primary candidate (skipping sample clips, PAR2,
// and NFO); for loose non-video releases (audio albums, books, comics, ROMs)
// it selects all non-auxiliary payload files.
func (e *Engine) selectArticleCheckFiles(ctx context.Context, rel *release) ([]*postedFile, []articleCheckSet, int) {
	var payload []*postedFile
	var payloadBytes int64
	var nonPar2 []*postedFile
	for _, pf := range rel.files {
		if pf.numSlots() == 0 {
			continue
		}
		rel.infoMu.RLock()
		info := pf.file.Info
		rel.infoMu.RUnlock()
		if info.Kind == nzb.KindPar2 {
			continue
		}
		nonPar2 = append(nonPar2, pf)
		if info.Sample || info.Kind == nzb.KindNFO || info.Kind == nzb.KindSFV || info.Kind == nzb.KindOther {
			continue
		}
		payload = append(payload, pf)
		payloadBytes += pf.file.Bytes()
	}

	b := &builder{e: e, rel: rel, prio: healthPriority(ctx)}
	cands := b.candidates(rel.files)
	if len(cands) > 0 && len(cands[0].files) > 0 && (payloadBytes == 0 || cands[0].bytes*2 >= payloadBytes) {
		missingVols := 0
		if cands[0].set != nil {
			missingVols = len(cands[0].set.Missing)
		}
		var sets []articleCheckSet
		if cands[0].kind == nzb.KindRAR || cands[0].kind == nzb.KindSplit {
			sets = []articleCheckSet{{kind: cands[0].kind, files: cands[0].files}}
		}
		return cands[0].files, sets, missingVols
	}
	selected := rel.files
	if len(payload) > 0 {
		selected = payload
	} else if len(nonPar2) > 0 {
		selected = nonPar2
	}
	sets, missingVols := archiveCheckSets(rel, selected)
	return selected, sets, missingVols
}

func archiveCheckSets(rel *release, files []*postedFile) ([]articleCheckSet, int) {
	byFile := make(map[*nzb.File]*postedFile, len(files))
	rel.infoMu.RLock()
	var vols []*nzb.File
	for _, pf := range files {
		byFile[pf.file] = pf
		if pf.file.Info.Kind.IsArchive() {
			vols = append(vols, pf.file)
		}
	}
	grouped := nzb.GroupSets(vols)
	rel.infoMu.RUnlock()
	missing := 0
	var out []articleCheckSet
	for _, s := range grouped {
		missing += len(s.Missing)
		if s.Kind != nzb.KindRAR && s.Kind != nzb.KindSplit {
			continue
		}
		pfs := make([]*postedFile, 0, len(s.Volumes))
		for _, f := range s.Volumes {
			if pf := byFile[f]; pf != nil {
				pfs = append(pfs, pf)
			}
		}
		if len(pfs) > 0 {
			out = append(out, articleCheckSet{kind: s.Kind, files: pfs})
		}
	}
	return out, missing
}
