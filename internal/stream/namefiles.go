package stream

import (
	"context"
	"fmt"
	"sort"

	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
)

// maxNamedFiles bounds how many unidentified files NameFiles probes: one
// article each, so a season pack of a few dozen episodes is covered.
const maxNamedFiles = 256

// NameFiles returns the NZB with real file names written into its subjects
// wherever PAR2 (or the first article's yEnc header) names an obfuscated
// file. A season pack can only be narrowed to one episode by name, so
// unlike Open (which only needs the main video) every unidentified file is
// probed. The release's PAR2 load and article cache are shared with Open.
// An NZB with nothing to name is returned unchanged. The result is kept on
// the release, so naming the same NZB again costs nothing.
func (e *Engine) NameFiles(ctx context.Context, releaseID string, nzbData []byte) ([]byte, error) {
	if err := e.ctx.Err(); err != nil {
		return nil, ErrClosed
	}
	rel, err := e.acquire(nzbData)
	if err != nil {
		return nil, err
	}
	defer e.release(rel)
	rel.namedMu.Lock()
	defer rel.namedMu.Unlock()
	if rel.named != nil {
		return rel.named, nil
	}
	b := &builder{e: e, rel: rel, prio: core.PriorityLive, fo: fetchOpts{cacheID: releaseID}}
	toName := b.namableFiles(maxNamedFiles)
	if len(toName) > 0 {
		sort.SliceStable(toName, func(i, j int) bool { return toName[i].idx < toName[j].idx })
		set := rel.loadPar2(ctx, b.prio, b.fo, rel.par2IndexFile())
		if _, _, err := b.probeObfuscated(ctx, toName, set); err != nil {
			return nil, fmt.Errorf("naming pack files: %w", err)
		}
	}
	rel.infoMu.Lock()
	defer rel.infoMu.Unlock()
	// An obfuscated PAR2 file found by its head keeps a name that says
	// nothing; give it a .par2 one so a later parse still classifies it.
	renamedPar2 := false
	for _, f := range rel.doc.Files {
		if f.Info.Kind == nzb.KindPar2 && nzb.Classify(f.Name).Kind != nzb.KindPar2 {
			f.Name = fmt.Sprintf("pack-%d.par2", f.Index)
			renamedPar2 = true
		}
	}
	if !renamedPar2 && !rel.hasRenamedFilesLocked() {
		rel.named = nzbData
		return nzbData, nil
	}
	out, ok := rel.doc.Subset(func(*nzb.File) bool { return true })
	if !ok {
		return nzbData, nil
	}
	rel.named = out
	return out, nil
}

// namableFiles lists files whose real names may come from PAR2 or yEnc:
// unidentified files (KindUnknown) and obfuscated non-PAR2 files (even if
// their obfuscated subject carried a known extension).
func (b *builder) namableFiles(limit int) []*postedFile {
	b.rel.infoMu.RLock()
	defer b.rel.infoMu.RUnlock()
	var out []*postedFile
	for _, pf := range b.rel.files {
		if pf.file.Info.Kind == nzb.KindUnknown || (pf.file.Obfuscated && pf.file.Info.Kind != nzb.KindPar2) {
			out = append(out, pf)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].file.Bytes() > out[j].file.Bytes() })
	if len(out) > limit {
		out = out[:max(limit, 0)]
	}
	return out
}

type fileIdentitySnapshot struct {
	name       string
	info       nzb.FileInfo
	obfuscated bool
}

type archiveSetSnapshot struct {
	kind    nzb.Kind
	volumes []*nzb.File
	dups    []*nzb.File
	missing int
}

type archiveRenameSnapshot struct {
	orig map[*nzb.File]fileIdentitySnapshot
	sets []archiveSetSnapshot
}

// snapshotArchiveOrder captures every file's identity and every multi-volume
// archive set's exact order before a rename pass runs.
func snapshotArchiveOrder(rel *release) archiveRenameSnapshot {
	rel.infoMu.RLock()
	defer rel.infoMu.RUnlock()
	snap := archiveRenameSnapshot{
		orig: make(map[*nzb.File]fileIdentitySnapshot, len(rel.files)),
	}
	for _, pf := range rel.files {
		f := pf.file
		snap.orig[f] = fileIdentitySnapshot{name: f.Name, info: f.Info, obfuscated: f.Obfuscated}
	}
	for _, s := range nzb.GroupSets(rel.doc.Files) {
		if len(s.Volumes) >= 2 {
			snap.sets = append(snap.sets, archiveSetSnapshot{
				kind:    s.Kind,
				volumes: append([]*nzb.File(nil), s.Volumes...),
				dups:    append([]*nzb.File(nil), s.Duplicates...),
				missing: len(s.Missing),
			})
		}
	}
	return snap
}

// guardArchiveRename validates post-rename archive sets against snap and cached
// RAR volume headers, reverting any rename that would reorder, split, collide,
// or introduce gaps in an archive set, or contradict cached RAR volume numbers.
func (b *builder) guardArchiveRename(ctx context.Context, snap archiveRenameSnapshot) map[*postedFile]bool {
	// Parse any already-cached RAR volume heads without holding rel.infoMu.
	b.rel.infoMu.RLock()
	var rarFiles []*postedFile
	for _, pf := range b.rel.files {
		if pf.file.Info.Kind == nzb.KindRAR {
			rarFiles = append(rarFiles, pf)
		}
	}
	b.rel.infoMu.RUnlock()

	rarVols := make(map[*nzb.File]*archive.RARVolume, len(rarFiles))
	for _, pf := range rarFiles {
		seg, ok := b.e.hot.get(segKey{b.rel.digest, pf.segBase})
		if !ok || seg == nil || seg.begin != 0 || !isRARFormat(archive.Detect(seg.data)) {
			continue
		}
		if v, err := b.readCachedRARVolume(ctx, pf, seg.data); err == nil {
			rarVols[pf.file] = v
		}
	}

	type restoreName struct {
		pf   *postedFile
		name string
	}
	var toRestore []restoreName
	reverted := map[*postedFile]bool{}

	b.rel.infoMu.Lock()
	byFile := make(map[*nzb.File]*postedFile, len(b.rel.files))
	for _, pf := range b.rel.files {
		byFile[pf.file] = pf
	}
	revert := func(f *nzb.File) {
		o, ok := snap.orig[f]
		if !ok {
			return
		}
		if f.Name == o.name && f.Info == o.info && f.Obfuscated == o.obfuscated {
			return
		}
		f.Name, f.Info, f.Obfuscated = o.name, o.info, o.obfuscated
		if pf := byFile[f]; pf != nil {
			reverted[pf] = true
			toRestore = append(toRestore, restoreName{pf: pf, name: o.name})
		}
	}
	nameChanged := func(f *nzb.File) bool {
		o, ok := snap.orig[f]
		return ok && f.Name != o.name
	}

	// Pass 1: every pre-existing multi-volume archive set must keep its exact
	// membership and relative volume order.
	newSets := nzb.GroupSets(b.rel.doc.Files)
	type setPos struct {
		set *nzb.Set
		pos int
	}
	inVols := make(map[*nzb.File]setPos)
	inDups := make(map[*nzb.File]*nzb.Set)
	for _, ns := range newSets {
		for idx, f := range ns.Volumes {
			inVols[f] = setPos{set: ns, pos: idx}
		}
		for _, f := range ns.Duplicates {
			inDups[f] = ns
		}
	}
	for _, os := range snap.sets {
		renamedInSet := false
		for _, f := range os.volumes {
			if nameChanged(f) {
				renamedInSet = true
				break
			}
		}
		inOS := make(map[*nzb.File]bool, len(os.volumes))
		for _, f := range os.volumes {
			inOS[f] = true
		}
		valid := true
		var ns *nzb.Set
		lastPos := -1
		for _, f := range os.volumes {
			if inDups[f] != nil {
				valid = false
				break
			}
			sp, ok := inVols[f]
			if !ok || sp.set == nil || sp.set.Kind != os.kind {
				valid = false
				break
			}
			if ns == nil {
				ns = sp.set
			} else if ns != sp.set {
				valid = false
				break
			}
			if sp.pos <= lastPos {
				valid = false
				break
			}
			lastPos = sp.pos
		}
		if valid && ns != nil {
			addedFromUnknown := false
			for _, g := range ns.Volumes {
				if inOS[g] {
					continue
				}
				if o, ok := snap.orig[g]; !ok || o.info.Kind != nzb.KindUnknown {
					valid = false
					break
				}
				addedFromUnknown = true
			}
			if valid && renamedInSet {
				if len(ns.Missing) > os.missing {
					valid = false
				}
				if !addedFromUnknown && len(ns.Volumes) != len(os.volumes) {
					valid = false
				}
			}
		}
		if !valid {
			for _, f := range os.volumes {
				revert(f)
			}
			for _, f := range os.dups {
				revert(f)
			}
			if ns != nil {
				for _, g := range ns.Volumes {
					if nameChanged(g) {
						revert(g)
					}
				}
				for _, g := range ns.Duplicates {
					if nameChanged(g) {
						revert(g)
					}
				}
			}
		}
	}

	// Pass 2: no newly renamed file may collide into duplicate volume numbers
	// or produce an archive volume order contradicting cached RAR headers.
	newSets = nzb.GroupSets(b.rel.doc.Files)
	for _, ns := range newSets {
		hasRenamed := false
		for _, f := range ns.Volumes {
			if nameChanged(f) {
				hasRenamed = true
				break
			}
		}
		if !hasRenamed {
			for _, f := range ns.Duplicates {
				if nameChanged(f) {
					hasRenamed = true
					break
				}
			}
		}
		if !hasRenamed {
			continue
		}
		if len(ns.Duplicates) > 0 {
			for _, f := range ns.Volumes {
				if nameChanged(f) {
					revert(f)
				}
			}
			for _, f := range ns.Duplicates {
				if nameChanged(f) {
					revert(f)
				}
			}
			continue
		}
		if ns.Kind == nzb.KindRAR && len(ns.Volumes) >= 2 {
			contradicts := false
			if v0 := rarVols[ns.Volumes[0]]; v0 != nil && rarVolumeIsContinuation(v0) {
				for k := 1; k < len(ns.Volumes); k++ {
					if vk := rarVols[ns.Volumes[k]]; vk != nil && (rarVolumeIsFirst(vk) || (v0.Number >= 0 && vk.Number >= 0 && vk.Number < v0.Number)) {
						contradicts = true
						break
					}
				}
			}
			for i := 0; i < len(ns.Volumes) && !contradicts; i++ {
				vi := rarVols[ns.Volumes[i]]
				if vi == nil {
					continue
				}
				for j := i + 1; j < len(ns.Volumes); j++ {
					vj := rarVols[ns.Volumes[j]]
					if vj == nil {
						continue
					}
					if (vi.Number >= 0 && vj.Number >= 0 && vi.Number >= vj.Number) || rarVolumeClaimsFirst(vj) {
						contradicts = true
						break
					}
				}
			}
			if contradicts {
				for _, f := range ns.Volumes {
					if nameChanged(f) {
						revert(f)
					}
				}
			}
		}
	}
	b.rel.infoMu.Unlock()

	for _, r := range toRestore {
		r.pf.setName(r.name)
	}
	return reverted
}
