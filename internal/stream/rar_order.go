package stream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync/atomic"

	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/nzb"
)

var (
	// errArchiveFirstVolumeMissing reports a RAR set whose leading volume is a
	// continuation and no volume in the set is volume 1.
	errArchiveFirstVolumeMissing = fmt.Errorf("stream: first archive volume missing: %w", archive.ErrMissingVolume)
	// errArchivePartsOutOfOrder reports an archive whose segments or volumes are
	// out of order and cannot be unambiguously reordered.
	errArchivePartsOutOfOrder = fmt.Errorf("stream: archive parts out of order: %w", ErrCorrupt)
)

type volumeOrderKind int

const (
	// volumeOrderOK means vols is usable in the returned order as native RAR
	// volumes (possibly with shifted yEnc baseOff when baseOff != nil).
	volumeOrderOK volumeOrderKind = iota
	// volumeOrderConcat means vols is a single RAR archive byte-split across
	// the returned volumes in order (with yEnc baseOff/chunkLen when shifted).
	volumeOrderConcat
)

type orderRAROpts struct {
	// splitSet is true for nzb.KindSplit sets (.001/.002, .rar.001/.rar.002),
	// where non-RAR payloads are allowed and subsequent chunks of a byte-split
	// single RAR legitimately start at yEnc offset 0 without a RAR signature.
	splitSet bool
	// maxFetches caps NNTP segment fetches (0 = unbounded, used by Open).
	maxFetches int
	// checkLast also inspects vols[n-1] during fast-path validation (used by Open).
	checkLast bool
	// checkStreamable checks checkRARVolume(v0) on an in-order volume 0 before
	// probing vols[1]/vols[n-1] so compressed sets fail fast in 1 fetch.
	checkStreamable bool
	// checkVideoCompressed fast-fails with archive.ErrCompressed when an
	// in-order volume 0's main video member is compressed or solid (used by
	// CheckArticles so compressed RARs cost only 1 article).
	checkVideoCompressed bool
}

type orderRARResult struct {
	vols      []*postedFile
	kind      volumeOrderKind
	baseOff   []int64
	chunkLen  []int64
	v0        *archive.RARVolume
	v1        *archive.RARVolume
	reordered bool
	fetches   int
}

// offsetReaderAt presents [base, base+size) of r as [0, size).
type offsetReaderAt struct {
	r    io.ReaderAt
	base int64
	size int64
}

func (o offsetReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= o.size {
		return 0, io.EOF
	}
	eof := false
	if int64(len(p)) > o.size-off {
		p = p[:o.size-off]
		eof = true
	}
	n, err := o.r.ReadAt(p, o.base+off)
	if err == nil && eof {
		err = io.EOF
	}
	return n, err
}

func isRARFormat(f archive.Format) bool {
	return f == archive.FormatRAR4 || f == archive.FormatRAR5
}

// rarVolumeIsContinuation reports whether v's header proves it is NOT volume 0.
func rarVolumeIsContinuation(v *archive.RARVolume) bool {
	if v == nil {
		return false
	}
	if len(v.Files) > 0 && v.Files[0].SplitBefore {
		return true
	}
	if v.Number > 0 {
		return true
	}
	if v.Format == archive.FormatRAR5 && !v.First {
		return true
	}
	if v.Format == archive.FormatRAR4 && v.MultiVolume && v.NewNumbering && !v.First {
		return true
	}
	return false
}

// rarVolumeIsFirst reports whether v can serve as the archive's first volume.
func rarVolumeIsFirst(v *archive.RARVolume) bool {
	if v == nil || rarVolumeIsContinuation(v) {
		return false
	}
	if v.First || v.Number == 0 {
		return true
	}
	// Older RAR4 (pre-3.0) volumes may omit MHD_FIRSTVOLUME; a volume whose
	// first file starts fresh (not SplitBefore) can still be volume 0.
	if v.Format == archive.FormatRAR4 && len(v.Files) > 0 && !v.Files[0].SplitBefore {
		return true
	}
	return false
}

// rarVolumeClaimsFirst reports whether a volume positively identifies itself as
// volume 0 (used on k > 0 when volume 0 already passed, avoiding false triggers
// on old RAR 2.x continuation volumes that happen to start a new file).
func rarVolumeClaimsFirst(v *archive.RARVolume) bool {
	return v != nil && !rarVolumeIsContinuation(v) && (v.First || v.Number == 0)
}

func rarVolumeIsLastSplit(v *archive.RARVolume) bool {
	if v == nil || len(v.Files) == 0 {
		return false
	}
	f := v.Files[0]
	return f.SplitBefore && !f.SplitAfter
}

func (b *builder) fetchCountedSeg(ctx context.Context, pf *postedFile, slot int, fetches *atomic.Int64) (*segData, error) {
	fetches.Add(1)
	return b.e.segment(ctx, b.rel, pf, slot, b.prio, b.fo)
}

func canBudget(fetches *atomic.Int64, maxFetches, need int) bool {
	if maxFetches <= 0 {
		return true
	}
	return int(fetches.Load())+need <= maxFetches
}

// readRARHead parses a volume's RAR header from seg0, optionally fetching slot 1
// (at most 1 extra article) when the header or leading member spans past seg0.
func (b *builder) readRARHead(ctx context.Context, pf *postedFile, seg0 *segData, maxFetches int, fetches *atomic.Int64) (*archive.RARVolume, error) {
	v, err := b.readCachedRARVolume(ctx, pf, seg0.data)
	if err == nil {
		needNext := false
		if len(v.Files) == 0 && !v.EndFound {
			needNext = true
		} else if len(v.Files) > 0 && !rarVolumeIsContinuation(v) && pickRARFile(v.Files, v.Format) == nil && !v.EndFound {
			last := v.Files[len(v.Files)-1]
			nextPos := last.DataOffset + last.PackedSize
			if !last.SplitAfter && nextPos >= int64(len(seg0.data)) && nextPos < int64(len(seg0.data))*2 {
				needNext = true
			}
		}
		if needNext && pf.file.SegmentCount() > 1 && canBudget(fetches, maxFetches, 1) {
			seg1, err1 := b.fetchCountedSeg(ctx, pf, 1, fetches)
			if err1 == nil && seg1 != nil && seg1.begin == seg0.end() {
				head := make([]byte, len(seg0.data)+len(seg1.data))
				copy(head, seg0.data)
				copy(head[len(seg0.data):], seg1.data)
				if v2, err2 := b.readCachedRARVolume(ctx, pf, head); err2 == nil {
					v = v2
				}
			}
		}
		return v, nil
	}
	if errors.Is(err, archive.ErrEncrypted) || errors.Is(err, archive.ErrPasswordRequired) {
		return nil, err
	}
	if pf.file.SegmentCount() > 1 && canBudget(fetches, maxFetches, 1) {
		seg1, err1 := b.fetchCountedSeg(ctx, pf, 1, fetches)
		if err1 != nil {
			return nil, err1
		}
		if seg1 != nil && seg1.begin == seg0.end() {
			head := make([]byte, len(seg0.data)+len(seg1.data))
			copy(head, seg0.data)
			copy(head[len(seg0.data):], seg1.data)
			return b.readCachedRARVolume(ctx, pf, head)
		}
	}
	return nil, err
}

// orderRARVolumes inspects a RAR (or split-RAR) volume set, reordering volumes
// by yEnc begin offsets or RAR volume numbers when out of order, and returning
// errArchiveFirstVolumeMissing or errArchivePartsOutOfOrder when unrecoverable.
// Both CheckArticles and Open (planRAR/planSplit) share this path.
func (b *builder) orderRARVolumes(ctx context.Context, vols []*postedFile, opts orderRAROpts) (orderRARResult, error) {
	n := len(vols)
	if n == 0 {
		return orderRARResult{}, nil
	}
	var fetches atomic.Int64
	resWith := func(r orderRARResult) orderRARResult {
		r.fetches = int(fetches.Load())
		return r
	}

	seg0, err := b.fetchCountedSeg(ctx, vols[0], 0, &fetches)
	if err != nil {
		return resWith(orderRARResult{vols: vols}), err
	}

	// Non-zero yEnc begin on volume 0: try reordering the set by yEnc begin.
	if seg0.begin != 0 {
		if n == 1 {
			return resWith(orderRARResult{vols: vols}), fmt.Errorf("stream: %s: first article begins at %d, not zero: %w", vols[0].Name(), seg0.begin, errArchivePartsOutOfOrder)
		}
		return b.reorderByYEnc(ctx, vols, map[int]*segData{0: seg0}, opts, &fetches)
	}

	sig0 := archive.Detect(seg0.data)
	if !isRARFormat(sig0) {
		if opts.splitSet {
			return resWith(orderRARResult{vols: vols, kind: volumeOrderOK}), nil
		}
		if n > 1 {
			return resWith(orderRARResult{vols: vols}), fmt.Errorf("stream: %s: missing RAR signature on leading volume: %w", vols[0].Name(), errArchivePartsOutOfOrder)
		}
		return resWith(orderRARResult{vols: vols, kind: volumeOrderOK}), nil
	}

	v0, err := b.readRARHead(ctx, vols[0], seg0, opts.maxFetches, &fetches)
	if err != nil {
		if errors.Is(err, archive.ErrEncrypted) || errors.Is(err, archive.ErrPasswordRequired) {
			return resWith(orderRARResult{vols: vols}), err
		}
		if errors.Is(err, archive.ErrCorrupt) || errors.Is(err, archive.ErrNotArchive) {
			// In unbounded Open mode, a volume with >2 segments might have a
			// very large archive comment/service block before the first file
			// header; let the full reader handle it instead of rejecting early.
			if opts.maxFetches == 0 && vols[0].file.SegmentCount() > 2 {
				return resWith(orderRARResult{vols: vols, kind: volumeOrderOK}), nil
			}
			return resWith(orderRARResult{vols: vols}), fmt.Errorf("stream: rar %s: %w", vols[0].Name(), errArchivePartsOutOfOrder)
		}
		return resWith(orderRARResult{vols: vols}), err
	}

	// Volume 0 is actually a continuation volume: try reordering by RAR volume numbers.
	if rarVolumeIsContinuation(v0) {
		if n == 1 {
			return resWith(orderRARResult{vols: vols, v0: v0}), errArchiveFirstVolumeMissing
		}
		return b.reorderByRARVolume(ctx, vols, map[int]*segData{0: seg0}, map[int]*archive.RARVolume{0: v0}, opts, &fetches)
	}

	// Fast-fail on an in-order volume 0 whose main video is unstreamable before
	// probing any additional volume heads.
	if opts.checkStreamable {
		if err := b.checkRARVolume(v0); err != nil {
			return resWith(orderRARResult{vols: vols, v0: v0}), err
		}
	}
	if opts.checkVideoCompressed {
		if reason := checkRARVolumeForCheck(v0); reason != "" {
			return resWith(orderRARResult{vols: vols, v0: v0}), archive.ErrCompressed
		}
	}

	if n == 1 {
		return resWith(orderRARResult{vols: vols, kind: volumeOrderOK, v0: v0}), nil
	}

	probeIdx := []int{1}
	if opts.checkLast && n > 2 {
		probeIdx = append(probeIdx, n-1)
	}
	if !canBudget(&fetches, opts.maxFetches, len(probeIdx)) {
		if canBudget(&fetches, opts.maxFetches, 1) {
			probeIdx = probeIdx[:1]
		} else {
			return resWith(orderRARResult{vols: vols, kind: volumeOrderOK, v0: v0}), nil
		}
	}

	probeSegs := make([]*segData, len(probeIdx))
	if err := b.parallel(ctx, len(probeIdx), func(ctx context.Context, i int) error {
		seg, err := b.fetchCountedSeg(ctx, vols[probeIdx[i]], 0, &fetches)
		if err != nil {
			return err
		}
		probeSegs[i] = seg
		return nil
	}); err != nil {
		return resWith(orderRARResult{vols: vols, v0: v0}), err
	}

	knownSegs := map[int]*segData{0: seg0}
	hasNonZeroBegin := false
	for i, idx := range probeIdx {
		knownSegs[idx] = probeSegs[i]
		if probeSegs[i].begin != 0 {
			hasNonZeroBegin = true
		}
	}
	if hasNonZeroBegin {
		return b.reorderByYEnc(ctx, vols, knownSegs, opts, &fetches)
	}

	sig1 := archive.Detect(probeSegs[0].data)
	if !isRARFormat(sig1) {
		if opts.splitSet && !v0.MultiVolume {
			return resWith(orderRARResult{vols: vols, kind: volumeOrderConcat, v0: v0}), nil
		}
		return resWith(orderRARResult{vols: vols, v0: v0}), fmt.Errorf("stream: %s: volume 2 lacks RAR signature: %w", vols[1].Name(), errArchivePartsOutOfOrder)
	}

	knownVols := map[int]*archive.RARVolume{0: v0}
	badOrder := false
	for i, idx := range probeIdx {
		seg := probeSegs[i]
		if !isRARFormat(archive.Detect(seg.data)) {
			return resWith(orderRARResult{vols: vols, v0: v0}), fmt.Errorf("stream: %s: volume %d lacks RAR signature: %w", vols[idx].Name(), idx+1, errArchivePartsOutOfOrder)
		}
		v, err := b.readRARHead(ctx, vols[idx], seg, opts.maxFetches, &fetches)
		if err != nil {
			if errors.Is(err, archive.ErrEncrypted) || errors.Is(err, archive.ErrPasswordRequired) {
				return resWith(orderRARResult{vols: vols, v0: v0}), err
			}
			if !errors.Is(err, archive.ErrCorrupt) && !errors.Is(err, archive.ErrNotArchive) {
				return resWith(orderRARResult{vols: vols, v0: v0}), err
			}
			continue
		}
		knownVols[idx] = v
		if rarVolumeClaimsFirst(v) || (v.Number >= 0 && v.Number != idx) {
			badOrder = true
		}
		if idx == n-1 && n > 2 {
			if main := pickRARFile(v0.Files, v0.Format); main != nil && main.SplitAfter &&
				len(v.Files) > 0 && v.Files[0].Name == main.Name && v.Files[0].SplitBefore && v.Files[0].SplitAfter {
				badOrder = true
			}
		}
	}
	if badOrder {
		return b.reorderByRARVolume(ctx, vols, knownSegs, knownVols, opts, &fetches)
	}
	return resWith(orderRARResult{vols: vols, kind: volumeOrderOK, v0: v0, v1: knownVols[1]}), nil
}

// reorderByYEnc attempts to reorder volumes by ascending yEnc begin offsets
// starting at 0, verifying contiguity across chunk boundaries.
func (b *builder) reorderByYEnc(ctx context.Context, vols []*postedFile, knownSegs map[int]*segData, opts orderRAROpts, fetches *atomic.Int64) (orderRARResult, error) {
	n := len(vols)
	resWith := func(r orderRARResult) orderRARResult {
		r.fetches = int(fetches.Load())
		return r
	}

	var missing []int
	for i := range n {
		if knownSegs[i] == nil {
			missing = append(missing, i)
		}
	}
	if !canBudget(fetches, opts.maxFetches, len(missing)) {
		return resWith(orderRARResult{vols: vols}), errArchivePartsOutOfOrder
	}

	segs := make([]*segData, n)
	for i, s := range knownSegs {
		segs[i] = s
	}
	if len(missing) > 0 {
		if err := b.parallel(ctx, len(missing), func(ctx context.Context, k int) error {
			idx := missing[k]
			seg, err := b.fetchCountedSeg(ctx, vols[idx], 0, fetches)
			if err != nil {
				return err
			}
			segs[idx] = seg
			return nil
		}); err != nil {
			return resWith(orderRARResult{vols: vols}), err
		}
	}

	order := make([]int, n)
	for i := range n {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		return segs[order[i]].begin < segs[order[j]].begin
	})

	if segs[order[0]].begin != 0 {
		return resWith(orderRARResult{vols: vols}), fmt.Errorf("stream: %s: no volume starts at yEnc offset 0 (min %d): %w", vols[0].Name(), segs[order[0]].begin, errArchivePartsOutOfOrder)
	}
	for k := 1; k < n; k++ {
		prev := segs[order[k-1]]
		cur := segs[order[k]]
		if cur.begin <= prev.begin || cur.begin < prev.end() {
			return resWith(orderRARResult{vols: vols}), fmt.Errorf("stream: overlapping or duplicate yEnc begin offsets (%d, %d): %w", prev.begin, cur.begin, errArchivePartsOutOfOrder)
		}
	}

	reordered := make([]*postedFile, n)
	baseOff := make([]int64, n)
	chunkLen := make([]int64, n)
	reorderedSegs := make([]*segData, n)
	for k, idx := range order {
		reordered[k] = vols[idx]
		reorderedSegs[k] = segs[idx]
		baseOff[k] = segs[idx].begin
	}

	if !isRARFormat(archive.Detect(reorderedSegs[0].data)) {
		return resWith(orderRARResult{vols: vols}), fmt.Errorf("stream: %s: offset 0 is not a RAR archive: %w", reordered[0].Name(), errArchivePartsOutOfOrder)
	}

	// Verify chunk end offsets and contiguity using each volume's last listed segment.
	var needTail []int
	for k, pf := range reordered {
		lastSlot := pf.file.SegmentCount() - 1
		if lastSlot <= 0 {
			end := reorderedSegs[k].end()
			if k < n-1 && end != baseOff[k+1] {
				return resWith(orderRARResult{vols: vols}), fmt.Errorf("stream: %s: yEnc chunk ends at %d, next starts at %d: %w", pf.Name(), end, baseOff[k+1], errArchivePartsOutOfOrder)
			}
			chunkLen[k] = end - baseOff[k]
			pf.trimUnlistedTail(end)
			continue
		}
		needTail = append(needTail, k)
	}

	if len(needTail) > 0 {
		toFetch := needTail
		if opts.maxFetches > 0 {
			avail := opts.maxFetches - int(fetches.Load())
			if avail < 0 {
				avail = 0
			}
			if len(toFetch) > avail {
				toFetch = toFetch[:avail]
			}
		}
		fetchedTail := make(map[int]bool, len(toFetch))
		tailSegs := make([]*segData, len(toFetch))
		if len(toFetch) > 0 {
			if err := b.parallel(ctx, len(toFetch), func(ctx context.Context, i int) error {
				k := toFetch[i]
				pf := reordered[k]
				lastSlot := pf.file.SegmentCount() - 1
				ls, err := b.fetchCountedSeg(ctx, pf, lastSlot, fetches)
				if err != nil {
					return err
				}
				tailSegs[i] = ls
				return nil
			}); err != nil {
				return resWith(orderRARResult{vols: vols}), err
			}
		}
		for i, k := range toFetch {
			ls := tailSegs[i]
			end := ls.end()
			if ls.begin < baseOff[k] || end <= baseOff[k] {
				return resWith(orderRARResult{vols: vols}), fmt.Errorf("stream: %s: non-monotonic yEnc tail [%d,%d) after begin %d: %w", reordered[k].Name(), ls.begin, end, baseOff[k], errArchivePartsOutOfOrder)
			}
			if k < n-1 && end != baseOff[k+1] {
				return resWith(orderRARResult{vols: vols}), fmt.Errorf("stream: %s: yEnc chunk ends at %d, next starts at %d: %w", reordered[k].Name(), end, baseOff[k+1], errArchivePartsOutOfOrder)
			}
			chunkLen[k] = end - baseOff[k]
			reordered[k].trimUnlistedTail(end)
			fetchedTail[k] = true
		}
		// Any remaining tails skipped only under CheckArticles budget cap:
		// infer chunk length from the adjacent yEnc begin offset and trim
		// unlisted phantom slots so stat sampling stays within real segments.
		for _, k := range needTail {
			if fetchedTail[k] {
				continue
			}
			if k < n-1 {
				chunkLen[k] = baseOff[k+1] - baseOff[k]
				reordered[k].trimUnlistedTail(baseOff[k+1])
			} else {
				reordered[k].trimUnlistedTail(0)
			}
		}
	}

	v0, err := b.readRARHead(ctx, reordered[0], reorderedSegs[0], opts.maxFetches, fetches)
	if err != nil {
		if errors.Is(err, archive.ErrEncrypted) || errors.Is(err, archive.ErrPasswordRequired) {
			return resWith(orderRARResult{vols: reordered, kind: volumeOrderConcat, baseOff: baseOff, chunkLen: chunkLen, reordered: true}), err
		}
		if errors.Is(err, archive.ErrCorrupt) || errors.Is(err, archive.ErrNotArchive) {
			return resWith(orderRARResult{vols: vols}), fmt.Errorf("stream: rar %s: %w", reordered[0].Name(), errArchivePartsOutOfOrder)
		}
		return resWith(orderRARResult{vols: vols}), err
	}
	if rarVolumeIsContinuation(v0) {
		return resWith(orderRARResult{vols: reordered, v0: v0}), errArchiveFirstVolumeMissing
	}

	allRAR := true
	for k := 1; k < n; k++ {
		if !isRARFormat(archive.Detect(reorderedSegs[k].data)) {
			allRAR = false
			break
		}
	}
	if allRAR {
		var v1 *archive.RARVolume
		for k := 1; k < n; k++ {
			vk, err := b.readCachedRARVolume(ctx, reordered[k], reorderedSegs[k].data)
			if err != nil {
				if errors.Is(err, archive.ErrEncrypted) || errors.Is(err, archive.ErrPasswordRequired) {
					return resWith(orderRARResult{vols: reordered, kind: volumeOrderOK, baseOff: baseOff, chunkLen: chunkLen, v0: v0, reordered: true}), err
				}
				return resWith(orderRARResult{vols: vols}), fmt.Errorf("stream: rar %s: %w", reordered[k].Name(), errArchivePartsOutOfOrder)
			}
			if rarVolumeClaimsFirst(vk) || (vk.Number >= 0 && vk.Number != k) {
				return resWith(orderRARResult{vols: vols}), errArchivePartsOutOfOrder
			}
			if k == 1 {
				v1 = vk
			}
		}
		return resWith(orderRARResult{
			vols:      reordered,
			kind:      volumeOrderOK,
			baseOff:   baseOff,
			chunkLen:  chunkLen,
			v0:        v0,
			v1:        v1,
			reordered: true,
		}), nil
	}

	return resWith(orderRARResult{
		vols:      reordered,
		kind:      volumeOrderConcat,
		baseOff:   baseOff,
		chunkLen:  chunkLen,
		v0:        v0,
		reordered: true,
	}), nil
}

// reorderByRARVolume probes volume headers (and RAR4 end-block volume numbers
// when needed) to reorder a multi-volume RAR set, or classify whether the first
// volume is missing vs parts are unorderable.
func (b *builder) reorderByRARVolume(ctx context.Context, vols []*postedFile, knownSegs map[int]*segData, knownVols map[int]*archive.RARVolume, opts orderRAROpts, fetches *atomic.Int64) (orderRARResult, error) {
	n := len(vols)
	resWith := func(r orderRARResult) orderRARResult {
		r.fetches = int(fetches.Load())
		return r
	}

	var missing []int
	for i := range n {
		if knownSegs[i] == nil {
			missing = append(missing, i)
		}
	}

	// If the set is larger than CheckArticles' probe budget, probe vols[1] and
	// vols[n-1] to distinguish a missing first volume (e.g. part00.rar omitted
	// from a 50-volume release) from scrambled parts.
	if !canBudget(fetches, opts.maxFetches, len(missing)) {
		return b.classifyLargeContinuationSet(ctx, vols, knownSegs, knownVols, opts, fetches)
	}

	segs := make([]*segData, n)
	for i, s := range knownSegs {
		segs[i] = s
	}
	if len(missing) > 0 {
		if err := b.parallel(ctx, len(missing), func(ctx context.Context, k int) error {
			idx := missing[k]
			seg, err := b.fetchCountedSeg(ctx, vols[idx], 0, fetches)
			if err != nil {
				return err
			}
			segs[idx] = seg
			return nil
		}); err != nil {
			return resWith(orderRARResult{vols: vols}), err
		}
	}

	// If any volume has a non-zero yEnc begin, try yEnc ordering instead.
	for i := range n {
		if segs[i].begin != 0 {
			allKnown := make(map[int]*segData, n)
			for j, s := range segs {
				allKnown[j] = s
			}
			return b.reorderByYEnc(ctx, vols, allKnown, opts, fetches)
		}
	}

	parsed := make([]*archive.RARVolume, n)
	for i := range n {
		if v := knownVols[i]; v != nil {
			parsed[i] = v
			continue
		}
		if !isRARFormat(archive.Detect(segs[i].data)) {
			return resWith(orderRARResult{vols: vols}), fmt.Errorf("stream: %s: volume lacks RAR signature: %w", vols[i].Name(), errArchivePartsOutOfOrder)
		}
		v, err := b.readRARHead(ctx, vols[i], segs[i], opts.maxFetches, fetches)
		if err != nil {
			if errors.Is(err, archive.ErrEncrypted) || errors.Is(err, archive.ErrPasswordRequired) {
				return resWith(orderRARResult{vols: vols}), err
			}
			if errors.Is(err, archive.ErrCorrupt) || errors.Is(err, archive.ErrNotArchive) {
				return resWith(orderRARResult{vols: vols}), fmt.Errorf("stream: rar %s: %w", vols[i].Name(), errArchivePartsOutOfOrder)
			}
			return resWith(orderRARResult{vols: vols}), err
		}
		parsed[i] = v
	}

	// Check whether any volume in the set can serve as volume 0.
	firstIdx := -1
	firstCount := 0
	for i, v := range parsed {
		if rarVolumeIsFirst(v) {
			firstIdx = i
			firstCount++
		}
	}
	if firstCount == 0 {
		return resWith(orderRARResult{vols: vols, v0: parsed[0]}), errArchiveFirstVolumeMissing
	}
	if firstCount > 1 {
		return resWith(orderRARResult{vols: vols, v0: parsed[0]}), errArchivePartsOutOfOrder
	}

	nums := make([]int, n)
	var needTail []int
	for i, v := range parsed {
		if v.Number >= 0 {
			nums[i] = v.Number
		} else if i == firstIdx {
			nums[i] = 0
		} else {
			nums[i] = -1
			if v.Format == archive.FormatRAR4 {
				if vols[i].file.SegmentCount() <= 1 {
					if tn := archive.ReadRAR4TailVolumeNumber(segs[i].data); tn >= 0 {
						nums[i] = tn
					}
				} else if n > 2 {
					needTail = append(needTail, i)
				}
			}
		}
	}

	// For RAR4 sets with >2 volumes lacking main-header volume numbers, read
	// the trailing 0x7b end block when budget permits.
	if len(needTail) > 0 && canBudget(fetches, opts.maxFetches, len(needTail)) {
		_ = b.parallel(ctx, len(needTail), func(ctx context.Context, k int) error {
			idx := needTail[k]
			pf := vols[idx]
			lastSlot := pf.file.SegmentCount() - 1
			tail, err := b.fetchCountedSeg(ctx, pf, lastSlot, fetches)
			if err == nil && tail != nil && len(tail.data) > 0 {
				if tn := archive.ReadRAR4TailVolumeNumber(tail.data); tn >= 0 {
					nums[idx] = tn
				}
			}
			return nil
		})
	}

	reorderedVols, reorderedParsed, err := orderByRARNumbers(vols, parsed, nums, firstIdx)
	if err != nil {
		return resWith(orderRARResult{vols: vols, v0: parsed[0]}), err
	}
	var v1 *archive.RARVolume
	if len(reorderedParsed) > 1 {
		v1 = reorderedParsed[1]
	}
	return resWith(orderRARResult{
		vols:      reorderedVols,
		kind:      volumeOrderOK,
		v0:        reorderedParsed[0],
		v1:        v1,
		reordered: true,
	}), nil
}

// classifyLargeContinuationSet handles sets larger than CheckArticles' probe
// budget when vols[0] is already known to be a continuation volume: inspect
// vols[1] and vols[n-1] to distinguish missing first volume from scrambled parts.
func (b *builder) classifyLargeContinuationSet(ctx context.Context, vols []*postedFile, knownSegs map[int]*segData, knownVols map[int]*archive.RARVolume, opts orderRAROpts, fetches *atomic.Int64) (orderRARResult, error) {
	n := len(vols)
	resWith := func(r orderRARResult) orderRARResult {
		r.fetches = int(fetches.Load())
		return r
	}
	v0 := knownVols[0]
	if !rarVolumeIsContinuation(v0) {
		return resWith(orderRARResult{vols: vols, v0: v0}), errArchivePartsOutOfOrder
	}

	probe := []int{}
	for _, idx := range []int{1, n - 1} {
		if idx > 0 && idx < n && knownSegs[idx] == nil {
			probe = append(probe, idx)
		}
	}
	if len(probe) > 0 && canBudget(fetches, opts.maxFetches, len(probe)) {
		segs := make([]*segData, len(probe))
		if err := b.parallel(ctx, len(probe), func(ctx context.Context, i int) error {
			seg, err := b.fetchCountedSeg(ctx, vols[probe[i]], 0, fetches)
			if err != nil {
				return err
			}
			segs[i] = seg
			return nil
		}); err != nil {
			return resWith(orderRARResult{vols: vols, v0: v0}), err
		}
		for i, idx := range probe {
			knownSegs[idx] = segs[i]
		}
	}

	for _, idx := range []int{1, n - 1} {
		if idx <= 0 || idx >= n {
			continue
		}
		seg := knownSegs[idx]
		if seg == nil {
			continue
		}
		if seg.begin != 0 || !isRARFormat(archive.Detect(seg.data)) {
			return resWith(orderRARResult{vols: vols, v0: v0}), errArchivePartsOutOfOrder
		}
		v := knownVols[idx]
		if v == nil {
			var err error
			v, err = b.readRARHead(ctx, vols[idx], seg, opts.maxFetches, fetches)
			if err != nil {
				if errors.Is(err, archive.ErrEncrypted) || errors.Is(err, archive.ErrPasswordRequired) {
					return resWith(orderRARResult{vols: vols, v0: v0}), err
				}
				return resWith(orderRARResult{vols: vols, v0: v0}), errArchivePartsOutOfOrder
			}
			knownVols[idx] = v
		}
		if rarVolumeIsFirst(v) {
			// Volume 0 exists in the set but out of order (and too large to
			// verify all volumes within CheckArticles' budget).
			return resWith(orderRARResult{vols: vols, v0: v0}), errArchivePartsOutOfOrder
		}
	}

	v1, vLast := knownVols[1], knownVols[n-1]
	// RAR5 or numbered RAR4: monotonic sequence starting past volume 0.
	if v0.Number > 0 {
		ok1 := v1 == nil || v1.Number == v0.Number+1
		okLast := vLast == nil || vLast.Number == v0.Number+n-1
		if ok1 && okLast {
			return resWith(orderRARResult{vols: vols, v0: v0}), errArchiveFirstVolumeMissing
		}
		return resWith(orderRARResult{vols: vols, v0: v0}), errArchivePartsOutOfOrder
	}
	// Unnumbered RAR4 continuations in natural split order (first/second both
	// SplitBefore+SplitAfter, last SplitBefore+!SplitAfter): first volume absent.
	if v1 != nil && vLast != nil &&
		rarVolumeIsContinuation(v1) && rarVolumeIsContinuation(vLast) &&
		rarVolumeIsLastSplit(vLast) {
		return resWith(orderRARResult{vols: vols, v0: v0}), errArchiveFirstVolumeMissing
	}
	return resWith(orderRARResult{vols: vols, v0: v0}), errArchivePartsOutOfOrder
}

// orderByRARNumbers produces an unambiguous ordering of vols/parsed using RAR
// volume numbers (or unique first/last roles for 2-3 volume unnumbered RAR4).
func orderByRARNumbers(vols []*postedFile, parsed []*archive.RARVolume, nums []int, firstIdx int) ([]*postedFile, []*archive.RARVolume, error) {
	n := len(vols)
	allNumbered := true
	for _, num := range nums {
		if num < 0 {
			allNumbered = false
			break
		}
	}
	if allNumbered {
		order := make([]int, n)
		for i := range n {
			order[i] = i
		}
		sort.Slice(order, func(i, j int) bool {
			return nums[order[i]] < nums[order[j]]
		})
		if nums[order[0]] > 0 {
			return nil, nil, errArchiveFirstVolumeMissing
		}
		for k := range n {
			if k > 0 && nums[order[k]] == nums[order[k-1]] {
				return nil, nil, fmt.Errorf("stream: duplicate RAR volume number %d: %w", nums[order[k]], errArchivePartsOutOfOrder)
			}
			if nums[order[k]] != k {
				return nil, nil, fmt.Errorf("stream: missing RAR volume %d: %w", k+1, archive.ErrMissingVolume)
			}
		}
		outVols := make([]*postedFile, n)
		outParsed := make([]*archive.RARVolume, n)
		for k, idx := range order {
			outVols[k] = vols[idx]
			outParsed[k] = parsed[idx]
		}
		return outVols, outParsed, nil
	}

	if n == 2 && firstIdx >= 0 {
		other := 1 - firstIdx
		return []*postedFile{vols[firstIdx], vols[other]}, []*archive.RARVolume{parsed[firstIdx], parsed[other]}, nil
	}
	if n == 3 && firstIdx >= 0 {
		lastIdx := -1
		lastCount := 0
		for i, v := range parsed {
			if i != firstIdx && rarVolumeIsLastSplit(v) {
				lastIdx = i
				lastCount++
			}
		}
		if lastCount == 1 {
			midIdx := 3 - firstIdx - lastIdx
			return []*postedFile{vols[firstIdx], vols[midIdx], vols[lastIdx]},
				[]*archive.RARVolume{parsed[firstIdx], parsed[midIdx], parsed[lastIdx]}, nil
		}
	}
	return nil, nil, errArchivePartsOutOfOrder
}

// reorderParsedRARVolumes verifies or reorders an already fully-parsed RAR set
// (used by rarFullScan so out-of-order middle volumes are also fixed).
func reorderParsedRARVolumes(vols []*postedFile, parsed []*archive.RARVolume) ([]*postedFile, []*archive.RARVolume, error) {
	n := len(vols)
	if n <= 1 || len(parsed) != n {
		return vols, parsed, nil
	}
	inOrder := !rarVolumeIsContinuation(parsed[0])
	if inOrder {
		for k, v := range parsed {
			if (k > 0 && rarVolumeClaimsFirst(v)) || (v.Number >= 0 && v.Number != k) {
				inOrder = false
				break
			}
		}
	}
	if inOrder {
		return vols, parsed, nil
	}
	firstIdx := -1
	firstCount := 0
	nums := make([]int, n)
	for i, v := range parsed {
		if rarVolumeIsFirst(v) {
			firstIdx = i
			firstCount++
		}
		nums[i] = v.Number
	}
	if firstCount == 0 {
		return nil, nil, errArchiveFirstVolumeMissing
	}
	if firstCount > 1 {
		return nil, nil, errArchivePartsOutOfOrder
	}
	if nums[firstIdx] < 0 {
		nums[firstIdx] = 0
	}
	return orderByRARNumbers(vols, parsed, nums, firstIdx)
}

// checkRARVolumeForCheck inspects a parsed RAR volume during CheckArticles and
// returns a permanent unplayable reason ("archive_compressed") when the volume's
// main video member cannot be streamed. Known non-video media (audio, books,
// comics, images) are skipped because non-video playback extracts compressed
// RARs sequentially.
func checkRARVolumeForCheck(v *archive.RARVolume) string {
	if v == nil {
		return ""
	}
	main := pickRARFile(v.Files, v.Format)
	if main == nil || nzb.IsNonVideoFile(main.Name) {
		return ""
	}
	if !main.Stored || main.Solid {
		return "archive_compressed"
	}
	return ""
}
