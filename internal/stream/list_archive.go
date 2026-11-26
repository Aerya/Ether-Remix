package stream

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
)

const (
	archiveListCacheMax   = 128
	archiveListCacheTTL   = 15 * time.Minute
	archiveListTimeout    = 15 * time.Second
	archiveListMaxBytes   = 16 << 20 // 16 MiB total header reads per NZB
	archiveListMaxSets    = 8
	archiveListMaxRARVols = 32
	archiveListMaxMembers = 4096
)

var errArchiveListBudget = errors.New("stream: archive member listing byte budget exceeded")

type archiveListCacheEntry struct {
	members   map[int][]core.ReleaseMember
	expiresAt time.Time
	lastUsed  time.Time
}

type archiveListFlight struct {
	done    chan struct{}
	members map[int][]core.ReleaseMember
	err     error
}

type budgetReaderAt struct {
	r         io.ReaderAt
	remaining *atomic.Int64
}

func (b budgetReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.remaining.Add(-int64(len(p))) < 0 {
		return 0, errArchiveListBudget
	}
	return b.r.ReadAt(p, off)
}

// ListArchiveMembers inspects RAR, 7z, and ZIP archives inside nzbData and
// returns non-directory inner members keyed by the first volume's NZB document
// index. Reads are strictly bounded (bytes, volume count, member count,
// timeout), deduplicated across concurrent callers, and cached by NZB digest.
// Individual unreadable or encrypted archives are skipped without failing the
// listing of other files.
func (e *Engine) ListArchiveMembers(ctx context.Context, nzbData []byte) (map[int][]core.ReleaseMember, error) {
	if e == nil || len(nzbData) == 0 {
		return nil, nil
	}
	if err := e.ctx.Err(); err != nil {
		return nil, ErrClosed
	}

	sum := sha256.Sum256(nzbData)
	key := hex.EncodeToString(sum[:])
	now := time.Now()

	e.archiveListMu.Lock()
	if ent := e.archiveList[key]; ent != nil && ent.expiresAt.After(now) {
		ent.lastUsed = now
		out := cloneReleaseMembersMap(ent.members)
		e.archiveListMu.Unlock()
		return out, nil
	}
	if fl := e.archiveListFlights[key]; fl != nil {
		e.archiveListMu.Unlock()
		select {
		case <-fl.done:
			return cloneReleaseMembersMap(fl.members), fl.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		e.archiveListMu.Unlock()
		return nil, err
	}
	if e.archiveListFlights == nil {
		e.archiveListFlights = map[string]*archiveListFlight{}
	}
	fl := &archiveListFlight{done: make(chan struct{})}
	e.archiveListFlights[key] = fl
	e.archiveListMu.Unlock()

	// Run the archive scan detached from the caller's deadline (e.g. a 5 s
	// plugin /resolve slice) so multi-volume RAR probes finish within
	// archiveListTimeout and cache the full member list for the next slice
	// instead of caching a caller-truncated partial scan.
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		e.archiveListMu.Lock()
		if e.archiveListFlights[key] == fl {
			delete(e.archiveListFlights, key)
		}
		fl.err = ErrClosed
		close(fl.done)
		e.archiveListMu.Unlock()
		return nil, ErrClosed
	}
	e.wg.Add(1)
	e.mu.Unlock()

	scanCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(e.ctx, cancel)
	dataCopy := bytes.Clone(nzbData)
	go func() {
		defer e.wg.Done()
		defer stop()
		defer cancel()
		var (
			members map[int][]core.ReleaseMember
			err     error
		)
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("stream: archive member listing panic: %v", p)
			}
			e.archiveListMu.Lock()
			if e.archiveListFlights[key] == fl {
				delete(e.archiveListFlights, key)
			}
			if err == nil && e.ctx.Err() == nil {
				if e.archiveList == nil {
					e.archiveList = map[string]*archiveListCacheEntry{}
				}
				now := time.Now()
				for k, ent := range e.archiveList {
					if !ent.expiresAt.After(now) {
						delete(e.archiveList, k)
					}
				}
				for len(e.archiveList) >= archiveListCacheMax {
					var oldestKey string
					var oldest *archiveListCacheEntry
					for k, ent := range e.archiveList {
						if oldest == nil || ent.lastUsed.Before(oldest.lastUsed) {
							oldestKey = k
							oldest = ent
						}
					}
					if oldest == nil {
						break
					}
					delete(e.archiveList, oldestKey)
				}
				e.archiveList[key] = &archiveListCacheEntry{
					members:   cloneReleaseMembersMap(members),
					expiresAt: now.Add(archiveListCacheTTL),
					lastUsed:  now,
				}
			}
			fl.members = members
			fl.err = err
			close(fl.done)
			e.archiveListMu.Unlock()
		}()
		members, err = e.scanArchiveMembers(scanCtx, dataCopy)
	}()

	select {
	case <-fl.done:
		return cloneReleaseMembersMap(fl.members), fl.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (e *Engine) scanArchiveMembers(ctx context.Context, nzbData []byte) (map[int][]core.ReleaseMember, error) {
	rel, err := e.acquire(nzbData)
	if err != nil {
		return nil, err
	}
	defer e.release(rel)

	var archFiles []*nzb.File
	byFile := make(map[*nzb.File]*postedFile, len(rel.files))
	fileIdx := make(map[*nzb.File]int, len(rel.files))
	for i, pf := range rel.files {
		byFile[pf.file] = pf
		fileIdx[pf.file] = i
		if pf.file.Info.Kind.IsArchive() {
			archFiles = append(archFiles, pf.file)
		}
	}
	sets := nzb.GroupSets(archFiles)
	if len(sets) == 0 {
		return nil, nil
	}
	if len(sets) > archiveListMaxSets {
		sets = sets[:archiveListMaxSets]
	}

	listCtx, cancel := context.WithTimeout(ctx, archiveListTimeout)
	defer cancel()
	listCtx = core.WithStreamKey(listCtx, rel)

	b := &builder{e: e, rel: rel, prio: core.PriorityLive}
	var remaining atomic.Int64
	remaining.Store(archiveListMaxBytes)
	wrap := func(r io.ReaderAt) io.ReaderAt {
		return budgetReaderAt{r: r, remaining: &remaining}
	}

	out := make(map[int][]core.ReleaseMember)
	for _, s := range sets {
		if listCtx.Err() != nil || remaining.Load() <= 0 {
			break
		}
		vols := make([]*postedFile, 0, len(s.Volumes))
		for _, v := range s.Volumes {
			if pf := byFile[v]; pf != nil {
				vols = append(vols, pf)
			}
		}
		if len(vols) == 0 {
			continue
		}
		ms := b.listArchiveSetMembers(listCtx, vols, wrap)
		if len(ms) > 0 {
			out[fileIdx[vols[0].file]] = ms
		}
	}
	return out, nil
}

func (b *builder) listArchiveSetMembers(ctx context.Context, vols []*postedFile, wrap func(io.ReaderAt) io.ReaderAt) []core.ReleaseMember {
	head, err := b.sniff(ctx, vols[0])
	if err != nil {
		return nil
	}
	switch archive.Detect(head) {
	case archive.FormatRAR4, archive.FormatRAR5:
		return b.listRARSetMembers(ctx, vols, wrap)
	case archive.Format7z:
		return b.list7zSetMembers(ctx, vols, head, wrap)
	case archive.FormatZIP:
		return b.listZIPSetMembers(ctx, vols, wrap)
	default:
		return nil
	}
}

func (b *builder) listRARSetMembers(ctx context.Context, vols []*postedFile, wrap func(io.ReaderAt) io.ReaderAt) []core.ReleaseMember {
	if err := b.heads(ctx, vols[:1]); err != nil {
		return nil
	}
	// Check whether multi-volume sets are native RAR volumes or byte-split
	// single RAR (.rar.001/.rar.002 where only volume 0 carries a RAR signature).
	if len(vols) > 1 {
		if head2, err := b.sniff(ctx, vols[1]); err == nil {
			f2 := archive.Detect(head2)
			if f2 != archive.FormatRAR4 && f2 != archive.FormatRAR5 {
				return b.listConcatRARMembers(ctx, vols, wrap)
			}
		}
	}

	limit := min(len(vols), archiveListMaxRARVols)
	parsed := make([]*archive.RARVolume, 0, limit)
	for k := 0; k < limit; k++ {
		if ctx.Err() != nil {
			break
		}
		if k > 0 {
			if err := b.heads(ctx, vols[k:k+1]); err != nil {
				break
			}
		}
		v, err := archive.ReadRARVolume(wrap(b.reader(ctx, vols[k])), vols[k].Size(), b.rarOptionsFor(ctx, vols[k], archiveListMaxMembers))
		if err != nil {
			break
		}
		parsed = append(parsed, v)
	}
	if len(parsed) == 0 {
		return nil
	}
	if members, err := archive.ComposeRAR(parsed); err == nil {
		return membersToReleaseMembers(members)
	}
	// Partial scan of a large multi-volume archive (stopped by volume or byte
	// budget): return deduplicated non-directory members seen in scanned volumes.
	return partialRARMembers(parsed)
}

func (b *builder) listConcatRARMembers(ctx context.Context, vols []*postedFile, wrap func(io.ReaderAt) io.ReaderAt) []core.ReleaseMember {
	if len(vols) > archiveListMaxRARVols {
		return nil
	}
	if err := b.heads(ctx, vols); err != nil {
		return nil
	}
	av := make([]archive.Volume, len(vols))
	var total int64
	for k, pf := range vols {
		s := pf.Size()
		if s < 0 || s > math.MaxInt64-total {
			return nil
		}
		av[k] = archive.Volume{R: wrap(b.reader(ctx, pf)), Size: s}
		total += s
	}
	v, err := archive.ReadRARVolume(archive.NewMultiReader(av), total, b.rarOptionsFor(ctx, vols[0], archiveListMaxMembers))
	if err != nil {
		return nil
	}
	members, err := archive.ComposeRAR([]*archive.RARVolume{v})
	if err != nil {
		return partialRARMembers([]*archive.RARVolume{v})
	}
	return membersToReleaseMembers(members)
}

func (b *builder) list7zSetMembers(ctx context.Context, vols []*postedFile, head []byte, wrap func(io.ReaderAt) io.ReaderAt) []core.ReleaseMember {
	if len(head) < 32 {
		return nil
	}
	n := len(vols)
	probe := []*postedFile{vols[0]}
	if n > 1 {
		probe = append(probe, vols[n-1])
	}
	if err := b.heads(ctx, probe); err != nil {
		return nil
	}
	last := vols[n-1]
	if !last.SizeKnown() {
		slot := last.numSlots() - 1
		ls, err := b.e.segment(ctx, b.rel, last, slot, b.prio, b.fo)
		if err != nil {
			return nil
		}
		last.learn(slot, ls.begin, ls.end(), ls.end(), 0)
	}
	want := 32 + int64(binary.LittleEndian.Uint64(head[12:20])) + int64(binary.LittleEndian.Uint64(head[20:28]))
	sizes := make([]int64, n)
	calcTotal := func(allKnown bool) int64 {
		var t int64
		for k := range vols {
			if k == 0 || k == n-1 || allKnown || vols[k].SizeKnown() {
				sizes[k] = vols[k].Size()
			} else {
				sizes[k] = vols[0].Size()
			}
			t += sizes[k]
		}
		return t
	}
	total := calcTotal(false)
	if total != want {
		if len(vols) > archiveListMaxRARVols {
			return nil
		}
		if err := b.heads(ctx, vols); err != nil {
			return nil
		}
		if total = calcTotal(true); total != want {
			return nil
		}
	}
	av := make([]archive.Volume, n)
	for k, pf := range vols {
		pf.setAssumed(sizes[k])
		av[k] = archive.Volume{R: wrap(b.reader(ctx, pf)), Size: sizes[k]}
	}
	members, err := archive.List7zWithPassword(ctx, archive.NewMultiReader(av), total, b.rel.doc.Password())
	if err != nil {
		return nil
	}
	return membersToReleaseMembers(members)
}

func (b *builder) listZIPSetMembers(ctx context.Context, vols []*postedFile, wrap func(io.ReaderAt) io.ReaderAt) []core.ReleaseMember {
	if len(vols) > archiveListMaxRARVols {
		return nil
	}
	if err := b.heads(ctx, vols); err != nil {
		return nil
	}
	last := vols[len(vols)-1]
	if !last.SizeKnown() {
		slot := last.numSlots() - 1
		ls, err := b.e.segment(ctx, b.rel, last, slot, b.prio, b.fo)
		if err != nil {
			return nil
		}
		last.learn(slot, ls.begin, ls.end(), ls.end(), 0)
	}
	av := make([]archive.Volume, len(vols))
	for k, pf := range vols {
		av[k] = archive.Volume{R: wrap(b.reader(ctx, pf)), Size: pf.Size()}
	}
	members, err := archive.ListZIP(av)
	if err != nil {
		return nil
	}
	return membersToReleaseMembers(members)
}

func membersToReleaseMembers(members []archive.Member) []core.ReleaseMember {
	out := make([]core.ReleaseMember, 0, min(len(members), archiveListMaxMembers))
	idx := 0
	for _, m := range members {
		if m.Dir {
			continue
		}
		out = append(out, core.ReleaseMember{
			Index:  idx,
			Path:   m.Name,
			Size:   m.Size,
			Stored: m.Stored,
		})
		idx++
		if len(out) >= archiveListMaxMembers {
			break
		}
	}
	return out
}

func partialRARMembers(vols []*archive.RARVolume) []core.ReleaseMember {
	seen := make(map[string]bool)
	var out []core.ReleaseMember
	idx := 0
	for _, v := range vols {
		for _, f := range v.Files {
			if f.Dir || f.Name == "" || seen[f.Name] {
				continue
			}
			seen[f.Name] = true
			out = append(out, core.ReleaseMember{
				Index:  idx,
				Path:   f.Name,
				Size:   f.UnpackedSize,
				Stored: f.Stored,
			})
			idx++
			if len(out) >= archiveListMaxMembers {
				return out
			}
		}
	}
	return out
}

func cloneReleaseMembersMap(in map[int][]core.ReleaseMember) map[int][]core.ReleaseMember {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int][]core.ReleaseMember, len(in))
	for k, v := range in {
		cp := make([]core.ReleaseMember, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}
