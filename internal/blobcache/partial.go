package blobcache

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// DefaultPartialTTL bounds how long an incomplete resumable download may
	// stay on disk without progress before Sweep evicts it.
	DefaultPartialTTL = 24 * time.Hour

	partialsDirName        = ".partials"
	partialDiskCheckStride = 1 << 20 // recheck live disk floor / shared cap every 1 MiB
)

// PartialMeta stores HTTP validator and expected-size metadata alongside an
// incomplete resumable blob. Partial blobs are never visible to Open,
// LookupKey, or CacheOpen until Commit completes.
type PartialMeta struct {
	ETag          string `json:"etag,omitempty"`
	LastModified  string `json:"lastModified,omitempty"`
	ContentLength int64  `json:"contentLength,omitempty"` // -1 when unknown
	Size          int64  `json:"size,omitempty"`
	UpdatedAt     int64  `json:"updatedAt,omitempty"` // UnixNano
}

// CommitResult is returned by Partial.Commit once a partial blob has been
// verified, hashed, and atomically promoted into the readable cache.
type CommitResult struct {
	Ref   Ref
	Size  int64
	CRC32 string
	MD5   string
	SHA1  string
}

type partialEntry struct {
	ID        string
	Size      int64
	UpdatedAt int64 // UnixNano
}

func hashPartialKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func (n *Namespace) partialsDir() string {
	return filepath.Join(n.dir, partialsDirName)
}

func (n *Namespace) partialDataPath(id string) string {
	return filepath.Join(n.partialsDir(), id+".partial")
}

func (n *Namespace) partialMetaPath(id string) string {
	return filepath.Join(n.partialsDir(), id+".meta.json")
}

func (n *Namespace) partialTTLLocked() time.Duration {
	if n.quota.TTL > 0 && n.quota.TTL < DefaultPartialTTL {
		return n.quota.TTL
	}
	return DefaultPartialTTL
}

func (n *Namespace) isPartialExpiredLocked(e *partialEntry, now time.Time) bool {
	if e == nil || e.UpdatedAt <= 0 {
		return false
	}
	ttl := n.partialTTLLocked()
	if ttl <= 0 {
		return false
	}
	updated := time.Unix(0, e.UpdatedAt)
	return !now.Before(updated.Add(ttl))
}

// loadPartials scans <ns.dir>/.partials on startup so surviving resumable
// downloads are accounted against namespace quotas, the shared cap, and the
// disk floor immediately, while orphaned or expired partial files are removed.
func (n *Namespace) loadPartials() {
	dir := n.partialsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	now := n.cache.now()
	dataSeen := make(map[string]int64)
	metaSeen := make(map[string]PartialMeta)
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		switch {
		case strings.HasSuffix(name, ".partial"):
			id := strings.TrimSuffix(name, ".partial")
			if len(id) != 64 {
				_ = os.Remove(filepath.Join(dir, name))
				continue
			}
			info, serr := de.Info()
			if serr != nil {
				_ = os.Remove(filepath.Join(dir, name))
				continue
			}
			dataSeen[id] = info.Size()
		case strings.HasSuffix(name, ".meta.json"):
			id := strings.TrimSuffix(name, ".meta.json")
			if len(id) != 64 {
				_ = os.Remove(filepath.Join(dir, name))
				continue
			}
			raw, rerr := os.ReadFile(filepath.Join(dir, name))
			if rerr != nil {
				_ = os.Remove(filepath.Join(dir, name))
				continue
			}
			var m PartialMeta
			if json.Unmarshal(raw, &m) != nil {
				_ = os.Remove(filepath.Join(dir, name))
				continue
			}
			metaSeen[id] = m
		default:
			_ = os.Remove(filepath.Join(dir, name))
		}
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	for id, diskSize := range dataSeen {
		meta, ok := metaSeen[id]
		if !ok || diskSize <= 0 || meta.Size <= 0 || diskSize < meta.Size || (meta.ContentLength >= 0 && meta.Size > meta.ContentLength) {
			_ = os.Remove(n.partialDataPath(id))
			_ = os.Remove(n.partialMetaPath(id))
			delete(metaSeen, id)
			continue
		}
		if diskSize > meta.Size {
			if os.Truncate(n.partialDataPath(id), meta.Size) == nil {
				diskSize = meta.Size
			}
		}
		pe := &partialEntry{ID: id, Size: diskSize, UpdatedAt: meta.UpdatedAt}
		if pe.UpdatedAt <= 0 {
			pe.UpdatedAt = now.UnixNano()
		}
		if n.isPartialExpiredLocked(pe, now) {
			_ = os.Remove(n.partialDataPath(id))
			_ = os.Remove(n.partialMetaPath(id))
			delete(metaSeen, id)
			continue
		}
		n.partials[id] = pe
		n.partialUsed += diskSize
		delete(metaSeen, id)
	}
	for id := range metaSeen {
		_ = os.Remove(n.partialMetaPath(id))
	}
}

func (n *Namespace) updatePartialLocked(id string, size, updatedAt int64) {
	if old, ok := n.partials[id]; ok {
		n.partialUsed = max(n.partialUsed-old.Size, 0)
	}
	if size <= 0 {
		delete(n.partials, id)
		return
	}
	n.partials[id] = &partialEntry{ID: id, Size: size, UpdatedAt: updatedAt}
	n.partialUsed += size
}

func (n *Namespace) evictPartial(id string) int64 {
	n.mu.Lock()
	if _, active := n.partialLocks[id]; active {
		n.mu.Unlock()
		return 0
	}
	pe, ok := n.partials[id]
	if !ok {
		n.mu.Unlock()
		return 0
	}
	delete(n.partials, id)
	n.partialUsed = max(n.partialUsed-pe.Size, 0)
	dataPath := n.partialDataPath(id)
	metaPath := n.partialMetaPath(id)
	n.mu.Unlock()

	_ = os.Remove(dataPath)
	_ = os.Remove(metaPath)
	return pe.Size
}

func (n *Namespace) writePartialMeta(id string, meta PartialMeta) error {
	if err := os.MkdirAll(n.partialsDir(), 0o755); err != nil {
		return fmt.Errorf("blobcache: mkdir partials: %w", err)
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	tmp := filepath.Join(n.partialsDir(), fmt.Sprintf(".%s.%d.tmp", id, n.tempSeq.Add(1)))
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, n.partialMetaPath(id)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// HostPartialBackend returns a resumable partial-blob opener suitable for
// wiring into plugins.KindBackends.CachePartial.
func (c *Cache) HostPartialBackend(quotaFor func(pluginID, libraryKey string) Quota) func(ctx context.Context, pluginID, libraryKey, key string) (*Partial, error) {
	return func(ctx context.Context, pluginID, libraryKey, key string) (*Partial, error) {
		c.BindPluginLibrary(pluginID, libraryKey)
		var q Quota
		if quotaFor != nil {
			q = quotaFor(pluginID, libraryKey)
		}
		ns := c.Namespace(libraryKey, q)
		return ns.OpenPartial(ctx, key)
	}
}

// Partial holds an exclusive handle on one incomplete resumable blob inside a
// Namespace. Bytes written to a Partial stay outside the readable blob index
// until Commit promotes the completed file into `<sha256[:2]>/<sha256>`.
type Partial struct {
	n    *Namespace
	id   string
	lock chan struct{}

	f         *os.File
	size      int64
	meta      PartialMeta
	committed bool
	discarded bool
	closed    bool

	capChecked    bool
	lastSharedCap int64
	sinceCapCheck int64
}

// OpenPartial acquires exclusive access to the resumable partial blob for key
// within n. If an unexpired, intact partial already exists on disk, it is
// reopened at its persisted byte offset; otherwise a fresh 0-byte partial is
// prepared. Callers must always call Close.
func (n *Namespace) OpenPartial(ctx context.Context, key string) (*Partial, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n.cache.mu.Lock()
	closed := n.cache.closed
	n.cache.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}

	id := hashPartialKey(key)
	var lockCh chan struct{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n.mu.Lock()
		waitCh, busy := n.partialLocks[id]
		if !busy {
			lockCh = make(chan struct{})
			n.partialLocks[id] = lockCh
			n.mu.Unlock()
			break
		}
		n.mu.Unlock()
		select {
		case <-waitCh:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	releaseLock := func() {
		n.mu.Lock()
		if n.partialLocks[id] == lockCh {
			delete(n.partialLocks, id)
		}
		n.mu.Unlock()
		close(lockCh)
	}

	if err := os.MkdirAll(n.partialsDir(), 0o755); err != nil {
		releaseLock()
		return nil, fmt.Errorf("blobcache: mkdir partials: %w", err)
	}

	p := &Partial{
		n:    n,
		id:   id,
		lock: lockCh,
		meta: PartialMeta{ContentLength: -1},
	}

	// Attempt to reopen an intact, unexpired partial from disk.
	dataPath := n.partialDataPath(id)
	metaPath := n.partialMetaPath(id)
	now := n.cache.now()
	reopened := false
	if raw, err := os.ReadFile(metaPath); err == nil {
		var meta PartialMeta
		if json.Unmarshal(raw, &meta) == nil && meta.Size > 0 && (meta.ContentLength < 0 || meta.Size <= meta.ContentLength) {
			n.mu.Lock()
			expired := n.isPartialExpiredLocked(&partialEntry{ID: id, Size: meta.Size, UpdatedAt: meta.UpdatedAt}, now)
			maxBytes := n.quota.EffectiveMaxBytes()
			n.mu.Unlock()
			sharedCap := n.cache.EffectiveSharedCap()
			if !expired && meta.Size <= maxBytes && meta.Size <= sharedCap {
				if st, serr := os.Stat(dataPath); serr == nil && !st.IsDir() && st.Size() >= meta.Size {
					f, oerr := os.OpenFile(dataPath, os.O_RDWR, 0o644)
					if oerr == nil {
						ok := true
						if st.Size() > meta.Size {
							if err := f.Truncate(meta.Size); err != nil {
								ok = false
							}
						}
						if ok {
							if _, err := f.Seek(meta.Size, io.SeekStart); err != nil {
								ok = false
							}
						}
						if ok {
							p.f = f
							p.size = meta.Size
							p.meta = meta
							reopened = true
							n.mu.Lock()
							n.updatePartialLocked(id, meta.Size, meta.UpdatedAt)
							n.mu.Unlock()
						} else {
							_ = f.Close()
						}
					}
				}
			}
		}
	}

	if !reopened {
		_ = os.Remove(dataPath)
		_ = os.Remove(metaPath)
		n.mu.Lock()
		n.updatePartialLocked(id, 0, 0)
		n.mu.Unlock()
	}

	return p, nil
}

// Size returns the number of bytes currently staged in the partial blob.
func (p *Partial) Size() int64 { return p.size }

// Meta returns a copy of the stored HTTP validators and expected length.
func (p *Partial) Meta() PartialMeta { return p.meta }

// SetMeta updates validators and expected length without truncating staged bytes.
func (p *Partial) SetMeta(meta PartialMeta) error {
	if p.closed || p.committed {
		return ErrClosed
	}
	meta.Size = p.size
	meta.UpdatedAt = p.n.cache.now().UnixNano()
	p.meta = meta
	if p.size > 0 {
		if err := p.n.writePartialMeta(p.id, p.meta); err != nil {
			return err
		}
		p.n.mu.Lock()
		p.n.updatePartialLocked(p.id, p.size, p.meta.UpdatedAt)
		p.n.mu.Unlock()
	}
	return nil
}

// Reset discards any staged bytes, resets the partial to offset 0, and records
// new metadata for a fresh transfer.
func (p *Partial) Reset(meta PartialMeta) error {
	if p.closed || p.committed {
		return ErrClosed
	}
	if p.f != nil {
		_ = p.f.Close()
		p.f = nil
	}
	dataPath := p.n.partialDataPath(p.id)
	_ = os.Remove(dataPath)
	_ = os.Remove(p.n.partialMetaPath(p.id))

	if err := os.MkdirAll(p.n.partialsDir(), 0o755); err != nil {
		return fmt.Errorf("blobcache: mkdir partials: %w", err)
	}
	f, err := os.OpenFile(dataPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("blobcache: create partial: %w", err)
	}
	p.f = f
	p.size = 0
	p.discarded = false
	p.capChecked = false
	p.sinceCapCheck = 0
	meta.Size = 0
	meta.UpdatedAt = p.n.cache.now().UnixNano()
	p.meta = meta

	p.n.mu.Lock()
	p.n.updatePartialLocked(p.id, 0, meta.UpdatedAt)
	p.n.mu.Unlock()
	return p.n.writePartialMeta(p.id, p.meta)
}

func (p *Partial) ensureSpace(add int64) error {
	if add <= 0 {
		return nil
	}
	p.n.cache.mu.Lock()
	closed := p.n.cache.closed
	p.n.cache.mu.Unlock()
	if closed {
		return ErrClosed
	}

	nextSize := p.size + add
	p.n.mu.Lock()
	maxBytes := p.n.quota.EffectiveMaxBytes()
	nsTotal := p.n.used + p.n.partialUsed
	p.n.mu.Unlock()

	if nextSize > maxBytes {
		return fmt.Errorf("%w: partial exceeded %d bytes", ErrTooLarge, maxBytes)
	}
	if nsTotal+add > maxBytes {
		p.n.Sweep()
		p.n.mu.Lock()
		maxBytes = p.n.quota.EffectiveMaxBytes()
		nsTotal = p.n.used + p.n.partialUsed
		p.n.mu.Unlock()
		if nextSize > maxBytes || nsTotal+add > maxBytes {
			return fmt.Errorf("%w: namespace quota %d bytes reached", ErrTooLarge, maxBytes)
		}
	}

	needCapCheck := !p.capChecked || p.sinceCapCheck >= partialDiskCheckStride || nextSize > p.lastSharedCap
	if !needCapCheck {
		totalUsed, _ := p.n.cache.TotalStats()
		if totalUsed+add > p.lastSharedCap {
			needCapCheck = true
		}
	}
	if needCapCheck {
		sharedCap := p.n.cache.EffectiveSharedCap()
		p.capChecked = true
		p.lastSharedCap = sharedCap
		p.sinceCapCheck = 0
		if nextSize > sharedCap {
			return fmt.Errorf("%w: partial exceeded cache cap %d bytes", ErrTooLarge, sharedCap)
		}
		totalUsed, _ := p.n.cache.TotalStats()
		if totalUsed+add > sharedCap {
			p.n.cache.enforceGlobalLimit()
			sharedCap = p.n.cache.EffectiveSharedCap()
			p.lastSharedCap = sharedCap
			totalUsed, _ = p.n.cache.TotalStats()
			if nextSize > sharedCap || totalUsed+add > sharedCap {
				return fmt.Errorf("%w: cache cap %d bytes reached", ErrTooLarge, sharedCap)
			}
		}
	}
	return nil
}

// Write appends b to the partial blob while enforcing the namespace quota,
// shared cache cap, and disk free-space floor.
func (p *Partial) Write(b []byte) (int, error) {
	if p.closed || p.committed {
		return 0, ErrClosed
	}
	if len(b) == 0 {
		return 0, nil
	}
	if err := p.ensureSpace(int64(len(b))); err != nil {
		return 0, err
	}
	if p.f == nil {
		if err := p.Reset(p.meta); err != nil {
			return 0, err
		}
	}
	n, err := p.f.Write(b)
	if n > 0 {
		p.size += int64(n)
		p.sinceCapCheck += int64(n)
		nowNs := p.n.cache.now().UnixNano()
		p.meta.Size = p.size
		p.meta.UpdatedAt = nowNs
		p.n.mu.Lock()
		p.n.updatePartialLocked(p.id, p.size, nowNs)
		p.n.mu.Unlock()
	}
	return n, err
}

// Sync persists the current byte count and validators to disk so a subsequent
// resume call continues from p.Size().
func (p *Partial) Sync() error {
	if p.closed || p.committed || p.discarded {
		return nil
	}
	if p.size <= 0 {
		return p.Discard()
	}
	nowNs := p.n.cache.now().UnixNano()
	p.meta.Size = p.size
	p.meta.UpdatedAt = nowNs
	if err := p.n.writePartialMeta(p.id, p.meta); err != nil {
		return err
	}
	p.n.mu.Lock()
	p.n.updatePartialLocked(p.id, p.size, nowNs)
	p.n.mu.Unlock()
	return nil
}

// Discard deletes the partial data and metadata files and immediately releases
// its quota accounting.
func (p *Partial) Discard() error {
	if p.closed || p.committed {
		return nil
	}
	if p.f != nil {
		_ = p.f.Close()
		p.f = nil
	}
	p.size = 0
	p.meta = PartialMeta{ContentLength: -1}
	p.discarded = true
	p.capChecked = false
	p.sinceCapCheck = 0
	_ = os.Remove(p.n.partialDataPath(p.id))
	_ = os.Remove(p.n.partialMetaPath(p.id))
	p.n.mu.Lock()
	p.n.updatePartialLocked(p.id, 0, 0)
	p.n.mu.Unlock()
	return nil
}

// Commit hashes the completed partial file in one pass, atomically promotes it
// into the readable blob cache under key, removes the partial metadata, sweeps
// if over quota, and returns its blob reference and digests.
func (p *Partial) Commit(ctx context.Context, key string) (CommitResult, error) {
	if err := ctx.Err(); err != nil {
		return CommitResult{}, err
	}
	if p.closed || p.committed {
		return CommitResult{}, ErrClosed
	}
	p.n.cache.mu.Lock()
	closed := p.n.cache.closed
	p.n.cache.mu.Unlock()
	if closed {
		return CommitResult{}, ErrClosed
	}

	p.n.mu.Lock()
	maxBytes := p.n.quota.EffectiveMaxBytes()
	p.n.mu.Unlock()
	sharedCap := p.n.cache.EffectiveSharedCap()
	limit := min(maxBytes, sharedCap)
	if limit <= 0 || p.size > maxBytes || p.size > sharedCap {
		return CommitResult{}, fmt.Errorf("%w: size %d exceeds limit %d", ErrTooLarge, p.size, limit)
	}

	dataPath := p.n.partialDataPath(p.id)
	if p.f == nil {
		if p.size != 0 {
			return CommitResult{}, fmt.Errorf("blobcache: partial file not open")
		}
		if err := os.MkdirAll(p.n.partialsDir(), 0o755); err != nil {
			return CommitResult{}, fmt.Errorf("blobcache: mkdir partials: %w", err)
		}
		f, err := os.OpenFile(dataPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
		if err != nil {
			return CommitResult{}, fmt.Errorf("blobcache: create empty partial: %w", err)
		}
		p.f = f
	}
	if err := p.f.Close(); err != nil {
		p.f = nil
		return CommitResult{}, fmt.Errorf("blobcache: close partial: %w", err)
	}
	p.f = nil

	rf, err := os.Open(dataPath)
	if err != nil {
		return CommitResult{}, fmt.Errorf("blobcache: open partial for hash: %w", err)
	}
	sha256H := sha256.New()
	sha1H := sha1.New()
	md5H := md5.New()
	crcH := crc32.NewIEEE()
	mw := io.MultiWriter(sha256H, sha1H, md5H, crcH)
	buf := make([]byte, 64<<10)
	var hashed int64
	for {
		if err := ctx.Err(); err != nil {
			_ = rf.Close()
			return CommitResult{}, err
		}
		nr, rerr := rf.Read(buf)
		if nr > 0 {
			_, _ = mw.Write(buf[:nr])
			hashed += int64(nr)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			_ = rf.Close()
			return CommitResult{}, rerr
		}
	}
	_ = rf.Close()

	if hashed != p.size {
		return CommitResult{}, fmt.Errorf("blobcache: partial size mismatch: got %d bytes, want %d", hashed, p.size)
	}

	digest := hex.EncodeToString(sha256H.Sum(nil))
	finalPath := p.n.blobPath(digest)
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return CommitResult{}, fmt.Errorf("blobcache: mkdir shard: %w", err)
	}
	if err := os.Rename(dataPath, finalPath); err != nil {
		// On Windows, renaming over an existing open identical content-addressed
		// blob can fail; if the exact target file already exists with matching
		// size, keep it and drop the staged file.
		if st, serr := os.Stat(finalPath); serr == nil && !st.IsDir() && st.Size() == hashed {
			_ = os.Remove(dataPath)
		} else {
			return CommitResult{}, fmt.Errorf("blobcache: rename partial blob: %w", err)
		}
	}
	_ = os.Remove(p.n.partialMetaPath(p.id))

	nowNs := p.n.cache.now().UnixNano()
	p.n.mu.Lock()
	p.n.updatePartialLocked(p.id, 0, 0)
	e, exists := p.n.blobs[digest]
	if !exists {
		e = &blobEntry{
			Hash:      digest,
			Size:      hashed,
			Name:      filepath.Base(key),
			CreatedAt: nowNs,
			LastOpen:  nowNs,
		}
		p.n.blobs[digest] = e
		p.n.used += hashed
	} else {
		if e.Size != hashed {
			p.n.used += hashed - e.Size
			e.Size = hashed
		}
		e.CreatedAt = nowNs
		e.LastOpen = nowNs
		if e.Name == "" && key != "" {
			e.Name = filepath.Base(key)
		}
	}
	if key != "" {
		p.n.keys[key] = digest
		if !containsString(e.Keys, key) {
			e.Keys = append(e.Keys, key)
		}
	}
	p.n.dirty = true
	overNamespace := p.n.used+p.n.partialUsed > p.n.quota.EffectiveMaxBytes()
	p.n.mu.Unlock()

	if overNamespace {
		p.n.Sweep()
	}
	p.n.cache.enforceGlobalLimit()
	p.n.flushIndex()

	p.committed = true
	return CommitResult{
		Ref:   Ref("blob:" + digest),
		Size:  hashed,
		CRC32: fmt.Sprintf("%08x", crcH.Sum32()),
		MD5:   fmt.Sprintf("%x", md5H.Sum(nil)),
		SHA1:  fmt.Sprintf("%x", sha1H.Sum(nil)),
	}, nil
}

// Close flushes metadata for any surviving partial bytes, closes the file
// descriptor, and releases the exclusive per-key lock.
func (p *Partial) Close() error {
	if p.closed {
		return nil
	}
	var err error
	if !p.committed && !p.discarded {
		if p.size <= 0 {
			_ = p.Discard()
		} else {
			err = p.Sync()
		}
	}
	if p.f != nil {
		if cerr := p.f.Close(); cerr != nil && err == nil {
			err = cerr
		}
		p.f = nil
	}
	p.closed = true
	p.n.mu.Lock()
	if p.n.partialLocks[p.id] == p.lock {
		delete(p.n.partialLocks, p.id)
	}
	p.n.mu.Unlock()
	close(p.lock)
	return err
}

// globalPartialCandidate pairs a namespace with one evictable idle partial.
type globalPartialCandidate struct {
	ns        *Namespace
	id        string
	updatedAt int64
	size      int64
}

func (c *Cache) collectIdlePartials(nss []*Namespace) []globalPartialCandidate {
	var out []globalPartialCandidate
	for _, ns := range nss {
		ns.mu.Lock()
		for id, pe := range ns.partials {
			if _, active := ns.partialLocks[id]; active {
				continue
			}
			out = append(out, globalPartialCandidate{
				ns:        ns,
				id:        id,
				updatedAt: pe.UpdatedAt,
				size:      pe.Size,
			})
		}
		ns.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].updatedAt != out[j].updatedAt {
			return out[i].updatedAt < out[j].updatedAt
		}
		return out[i].id < out[j].id
	})
	return out
}

// sweepPartialsLocked collects expired idle partials (and, when needed, oldest
// idle partials to bring used+partialUsed back under maxBytes) for removal
// outside n.mu. Caller must hold n.mu.
func (n *Namespace) sweepPartialsLocked(now time.Time, maxBytes int64) []string {
	var paths []string
	var active []*partialEntry
	for id, pe := range n.partials {
		if _, locked := n.partialLocks[id]; locked {
			continue
		}
		if n.isPartialExpiredLocked(pe, now) {
			delete(n.partials, id)
			n.partialUsed = max(n.partialUsed-pe.Size, 0)
			paths = append(paths, n.partialDataPath(id), n.partialMetaPath(id))
			continue
		}
		cp := *pe
		active = append(active, &cp)
	}
	if n.used+n.partialUsed > maxBytes && len(active) > 0 {
		sort.Slice(active, func(i, j int) bool {
			if active[i].UpdatedAt != active[j].UpdatedAt {
				return active[i].UpdatedAt < active[j].UpdatedAt
			}
			return active[i].ID < active[j].ID
		})
		for _, pe := range active {
			if n.used+n.partialUsed <= maxBytes {
				break
			}
			delete(n.partials, pe.ID)
			n.partialUsed = max(n.partialUsed-pe.Size, 0)
			paths = append(paths, n.partialDataPath(pe.ID), n.partialMetaPath(pe.ID))
		}
	}
	return paths
}

// PartialStats returns the current bytes and count of incomplete resumable
// partials in this namespace.
func (n *Namespace) PartialStats() (bytes int64, count int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.partialUsed, len(n.partials)
}
