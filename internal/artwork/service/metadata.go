package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"image"
	"strconv"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/artwork"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/lru"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("artwork_metadata", 1, `CREATE TABLE artwork_image_metadata (image_key TEXT PRIMARY KEY, width INTEGER NOT NULL, height INTEGER NOT NULL, blurhash TEXT NOT NULL);
CREATE TABLE artwork_image_metadata_claims (image_key TEXT PRIMARY KEY, token TEXT NOT NULL, expires_at INTEGER NOT NULL);`)
	store.Register("artwork_metadata", 2, `ALTER TABLE artwork_image_metadata ADD COLUMN thumbhash TEXT NOT NULL DEFAULT '';`)
}

// MetadataQueue joins stored width, height, and hash rows on the request.
// Missing descriptors are hashed by the background worker. A caller with a
// deadline may wait for a hash already in flight, and never longer than
// artworkInflightWait.
type MetadataQueue struct {
	ctx     context.Context
	workers sync.WaitGroup
	db      *sql.DB
	active  func(context.Context, int64) (*core.User, error)
	read    func(context.Context, frontend.ArtworkRequest, frontend.Image, frontend.ArtworkTransform) ([]byte, string, error)
	jobs    chan artworkMetadataJob
	mu      sync.Mutex
	cache   map[string]artworkMetadata
	pending map[string]bool
	retry   map[string]time.Time
	// touches carries keys whose used_at is a day old (see touch).
	touches chan string
	// persist reports whether an item's metadata may be written; nil
	// writes everything. Titles with no stored row (search and list hits)
	// keep theirs in memory only: a search page must not write rows.
	persist      func(context.Context, core.RatingKey) bool
	persistBatch func(context.Context, []core.RatingKey) map[core.RatingKey]bool
	memory       *lru.Cache[string, artworkMetadata] // unpersisted hashes, guarded by mu
}

// memoryMetadataEntries bounds the hashes kept for titles with no row
// (about 200 bytes each). metadataCacheMax matches it for persisted rows.
const (
	memoryMetadataEntries = 16 << 10
	metadataCacheMax      = 16 << 10
)

// PersistFor limits database writes to items persist accepts; the rest
// keep their hashes in a bounded memory cache. Call before serving.
func (q *MetadataQueue) PersistFor(persist func(context.Context, core.RatingKey) bool) {
	if q != nil {
		q.mu.Lock()
		q.persist = persist
		q.mu.Unlock()
	}
}

// PersistBatchFor supplies a batched counterpart to PersistFor so page reads
// check persistence in chunked queries rather than one SELECT per image.
func (q *MetadataQueue) PersistBatchFor(persistBatch func(context.Context, []core.RatingKey) map[core.RatingKey]bool) {
	if q != nil {
		q.mu.Lock()
		q.persistBatch = persistBatch
		q.mu.Unlock()
	}
}

type preloadedPersistKey struct{}
type preloadedMetadataMissKey struct{}
type preloadedMetadataPartialKey struct{}

type artworkMetadataLookup struct {
	request frontend.ArtworkRequest
	image   frontend.Image
}

func (q *MetadataQueue) checkPersistBatch(ctx context.Context, keys []core.RatingKey) map[core.RatingKey]bool {
	unique := make([]core.RatingKey, 0, len(keys))
	seen := make(map[core.RatingKey]bool, len(keys))
	for _, k := range keys {
		if k == 0 || seen[k] {
			continue
		}
		seen[k] = true
		unique = append(unique, k)
	}
	if len(unique) == 0 {
		return nil
	}
	q.mu.Lock()
	persist := q.persist
	persistBatch := q.persistBatch
	q.mu.Unlock()
	if persistBatch != nil {
		return persistBatch(ctx, unique)
	}
	out := make(map[core.RatingKey]bool, len(unique))
	if persist == nil {
		for _, k := range unique {
			out[k] = true
		}
		return out
	}
	for _, k := range unique {
		out[k] = persist(ctx, k)
	}
	return out
}

// Preload resolves stored artwork metadata and stale-row persistence checks for
// a batch of images in chunked queries so page listing never issues a query per
// image or per title.
func (q *MetadataQueue) Preload(ctx context.Context, items []artworkMetadataLookup) context.Context {
	if q == nil || q.db == nil || q.active == nil || q.read == nil || q.ctx.Err() != nil || len(items) == 0 {
		return ctx
	}
	now := time.Now().Unix()
	touchWindow := int64(metadataTouchEvery / time.Second)
	type staleEntry struct {
		key string
		req frontend.ArtworkRequest
		md  artworkMetadata
	}
	type missEntry struct {
		key string
		req frontend.ArtworkRequest
	}
	var staleCached []staleEntry
	var misses []missEntry
	seen := make(map[string]bool, len(items))

	q.mu.Lock()
	for _, item := range items {
		if item.request.Viewer == nil {
			continue
		}
		key := artworkMetadataKey(item.request, item.image)
		if seen[key] {
			continue
		}
		seen[key] = true
		if cached, ok := q.cache[key]; ok && cached.thumbhash != "" {
			if now-cached.usedAt >= touchWindow {
				staleCached = append(staleCached, staleEntry{key: key, req: item.request, md: cached})
			}
			continue
		}
		if _, ok := q.memory.Get(key); ok {
			continue
		}
		misses = append(misses, missEntry{key: key, req: item.request})
	}
	q.mu.Unlock()

	if len(staleCached) == 0 && len(misses) == 0 {
		return ctx
	}

	persistMap := map[core.RatingKey]bool{}
	checkedMisses := map[string]bool{}
	partialLoaded := map[string]artworkMetadata{}

	var pluginKeys []core.RatingKey
	for _, m := range misses {
		if m.req.Item.IsPluginKey() {
			pluginKeys = append(pluginKeys, m.req.Item)
		}
	}
	if len(pluginKeys) > 0 {
		checkCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		for k, v := range q.checkPersistBatch(checkCtx, pluginKeys) {
			persistMap[k] = v
		}
		cancel()
	}

	var dbMisses []missEntry
	for _, m := range misses {
		if m.req.Item.IsPluginKey() {
			if p, known := persistMap[m.req.Item]; known && !p {
				checkedMisses[m.key] = true
				continue
			}
		}
		dbMisses = append(dbMisses, m)
	}

	var loaded []staleEntry
	if len(dbMisses) > 0 {
		loadCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		for part := range store.Chunks(dbMisses, 200) {
			args := make([]any, len(part))
			for i, m := range part {
				args[i] = m.key
			}
			rows, err := q.db.QueryContext(loadCtx, `SELECT image_key,width,height,blurhash,thumbhash,used_at FROM artwork_image_metadata WHERE image_key IN (`+placeholders(len(part))+`)`, args...)
			if err != nil {
				continue
			}
			found := make(map[string]artworkMetadata, len(part))
			for rows.Next() {
				var k string
				var md artworkMetadata
				if err := rows.Scan(&k, &md.width, &md.height, &md.blurhash, &md.thumbhash, &md.usedAt); err == nil {
					found[k] = md
				}
			}
			rowsErr := rows.Err()
			rows.Close()
			if rowsErr != nil {
				continue
			}
			for _, m := range part {
				if md, ok := found[m.key]; ok && (md.blurhash != "" || md.thumbhash != "") {
					loaded = append(loaded, staleEntry{key: m.key, req: m.req, md: md})
				} else {
					checkedMisses[m.key] = true
				}
			}
		}
		cancel()
	}

	var needPersist []core.RatingKey
	for _, sc := range staleCached {
		if sc.req.Item != 0 {
			if _, known := persistMap[sc.req.Item]; !known {
				needPersist = append(needPersist, sc.req.Item)
			}
		}
	}
	for _, ld := range loaded {
		if ld.req.Item != 0 && now-ld.md.usedAt >= touchWindow {
			if _, known := persistMap[ld.req.Item]; !known {
				needPersist = append(needPersist, ld.req.Item)
			}
		}
	}
	if len(needPersist) > 0 {
		checkCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		for k, v := range q.checkPersistBatch(checkCtx, needPersist) {
			persistMap[k] = v
		}
		cancel()
	}

	q.mu.Lock()
	for _, sc := range staleCached {
		if sc.req.Item != 0 {
			p, known := persistMap[sc.req.Item]
			if !known {
				continue
			}
			if !p {
				delete(q.cache, sc.key)
				q.memory.Add(sc.key, sc.md)
				continue
			}
		}
		md := sc.md
		if cached, ok := q.cache[sc.key]; ok && cached.usedAt > md.usedAt {
			md.usedAt = cached.usedAt
		}
		q.touch(sc.key, &md)
		q.cache[sc.key] = md
	}
	for _, ld := range loaded {
		if ld.req.Item != 0 && now-ld.md.usedAt >= touchWindow {
			p, known := persistMap[ld.req.Item]
			if !known {
				continue
			}
			if !p {
				delete(q.cache, ld.key)
				q.memory.Add(ld.key, ld.md)
				continue
			}
		}
		md := ld.md
		if cached, ok := q.cache[ld.key]; ok && cached.usedAt > md.usedAt {
			md.usedAt = cached.usedAt
		}
		q.touch(ld.key, &md)
		if len(q.cache) >= metadataCacheMax {
			q.cache = map[string]artworkMetadata{}
		}
		q.cache[ld.key] = md
		if md.thumbhash == "" {
			partialLoaded[ld.key] = md
		}
	}
	q.mu.Unlock()

	if len(persistMap) > 0 {
		ctx = context.WithValue(ctx, preloadedPersistKey{}, persistMap)
	}
	if len(checkedMisses) > 0 {
		ctx = context.WithValue(ctx, preloadedMetadataMissKey{}, checkedMisses)
	}
	if len(partialLoaded) > 0 {
		ctx = context.WithValue(ctx, preloadedMetadataPartialKey{}, partialLoaded)
	}
	return ctx
}

func (q *MetadataQueue) persistable(ctx context.Context, req frontend.ArtworkRequest) bool {
	if req.Item == 0 {
		return true
	}
	if preloaded, ok := ctx.Value(preloadedPersistKey{}).(map[core.RatingKey]bool); ok {
		if v, found := preloaded[req.Item]; found {
			return v
		}
	}
	q.mu.Lock()
	persist := q.persist
	q.mu.Unlock()
	if persist == nil {
		return true
	}
	if reqmemo.Active(ctx) {
		v, _ := reqmemo.Do(ctx, "artwork:persist:"+req.Item.String(), func() (bool, error) {
			return persist(ctx, req.Item), nil
		})
		return v
	}
	return persist(ctx, req.Item)
}

type artworkMetadata struct {
	width, height int
	blurhash      string
	thumbhash     string
	// usedAt is the row's used_at as last read or touched (Unix seconds).
	usedAt int64
}
type artworkMetadataJob struct {
	key     string
	request frontend.ArtworkRequest
	image   frontend.Image
}

func NewMetadataQueue(ctx context.Context, db *sql.DB, active func(context.Context, int64) (*core.User, error), read func(context.Context, frontend.ArtworkRequest, frontend.Image, frontend.ArtworkTransform) ([]byte, string, error)) *MetadataQueue {
	q := &MetadataQueue{ctx: ctx, db: db, active: active, read: read, jobs: make(chan artworkMetadataJob, 128), cache: map[string]artworkMetadata{}, pending: map[string]bool{}, retry: map[string]time.Time{}, touches: make(chan string, 2048),
		memory: lru.New(lru.Options[string, artworkMetadata]{MaxEntries: memoryMetadataEntries})}
	for i := 0; i < 2; i++ {
		q.workers.Add(1)
		go q.run()
	}
	q.workers.Add(1)
	go q.runTouches()
	return q
}

func artworkMetadataKey(req frontend.ArtworkRequest, img frontend.Image) string {
	h := sha256.New()
	viewer := int64(0)
	if req.Viewer != nil {
		viewer = req.Viewer.ID
	}
	// Personalized overlays can render differently for the same underlying
	// descriptor, so keep the viewer scope even for other artwork providers.
	// Variant/language selection must be represented by the descriptor tag;
	// identical descriptors share metadata across a viewer's devices.
	for _, part := range []string{"blurhash-v1", strconv.FormatInt(viewer, 10), req.Item.String(), req.Channel,
		img.Kind, img.Path, img.Tag, img.Subject} {
		// Length prefix prevents ambiguous identities even for unusual strings.
		h.Write([]byte(strconv.Itoa(len(part))))
		h.Write([]byte{':'})
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

const artworkInflightWait = 100 * time.Millisecond

func (q *MetadataQueue) Get(ctx context.Context, req frontend.ArtworkRequest, img frontend.Image) frontend.Image {
	if q == nil || q.db == nil || q.active == nil || q.read == nil || req.Viewer == nil || q.ctx.Err() != nil {
		return img
	}
	key := artworkMetadataKey(req, img)
	stored, have := q.stored(ctx, req, key)
	if have && stored.thumbhash != "" {
		return applyArtworkMetadata(img, stored)
	}
	q.enqueue(req, img, key)
	if have {
		img = applyArtworkMetadata(img, stored)
	}
	wait, ok := synchronousArtworkWait(ctx)
	if !ok {
		return img
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if stored, ok := q.stored(ctx, req, key); ok {
			return applyArtworkMetadata(img, stored)
		}
		select {
		case <-ctx.Done():
			return img
		case <-timer.C:
			return img
		case <-tick.C:
		}
	}
}

func applyArtworkMetadata(img frontend.Image, metadata artworkMetadata) frontend.Image {
	img.Width, img.Height, img.BlurHash, img.ThumbHash = metadata.width, metadata.height, metadata.blurhash, metadata.thumbhash
	return img
}

func (q *MetadataQueue) stored(ctx context.Context, req frontend.ArtworkRequest, key string) (artworkMetadata, bool) {
	q.mu.Lock()
	if cached, ok := q.cache[key]; ok && cached.thumbhash != "" {
		q.mu.Unlock()
		if time.Now().Unix()-cached.usedAt >= int64(metadataTouchEvery/time.Second) {
			q.remember(ctx, req, key, cached)
		}
		return cached, true
	}
	if held, ok := q.memory.Get(key); ok {
		q.mu.Unlock()
		return held, true
	}
	q.mu.Unlock()
	if misses, ok := ctx.Value(preloadedMetadataMissKey{}).(map[string]bool); ok && misses[key] {
		return artworkMetadata{}, false
	}
	if partial, ok := ctx.Value(preloadedMetadataPartialKey{}).(map[string]artworkMetadata); ok {
		if m, found := partial[key]; found {
			return m, true
		}
	}
	if req.Item.IsPluginKey() && !q.persistable(ctx, req) {
		return artworkMetadata{}, false
	}
	// A free lookup is a cheap join. A locked database must not hold the page.
	loadCtx, cancel := context.WithTimeout(context.Background(), 8*time.Millisecond)
	metadata, err := q.load(loadCtx, key)
	cancel()
	if err != nil || (metadata.blurhash == "" && metadata.thumbhash == "") {
		return artworkMetadata{}, false
	}
	q.remember(ctx, req, key, metadata)
	return metadata, true
}

func (q *MetadataQueue) remember(ctx context.Context, req frontend.ArtworkRequest, key string, metadata artworkMetadata) {
	// Metadata can outlive a title removed from the library by tier cleanup.
	// Check current persistence before refreshing that row, outside the mutex.
	// Fresh cache hits and memory-only hashes require no additional lookup.
	if time.Now().Unix()-metadata.usedAt >= int64(metadataTouchEvery/time.Second) {
		checkCtx, cancel := context.WithTimeout(ctx, 8*time.Millisecond)
		persist := q.persistable(checkCtx, req)
		cancel()
		if !persist {
			q.mu.Lock()
			delete(q.cache, key)
			q.memory.Add(key, metadata)
			q.mu.Unlock()
			return
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if cached, ok := q.cache[key]; ok && cached.usedAt > metadata.usedAt {
		metadata.usedAt = cached.usedAt
	}
	q.touch(key, &metadata)
	if len(q.cache) >= metadataCacheMax {
		q.cache = map[string]artworkMetadata{}
	}
	q.cache[key] = metadata
}

func (q *MetadataQueue) enqueue(req frontend.ArtworkRequest, img frontend.Image, key string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending[key] || time.Now().Before(q.retry[key]) || len(q.pending) >= cap(q.jobs) {
		return
	}
	// Only the ID is retained. Current membership and policy are checked by
	// the worker before any bytes are fetched.
	req.Viewer = &core.User{ID: req.Viewer.ID}
	select {
	case q.jobs <- artworkMetadataJob{key: key, request: req, image: img}:
		q.pending[key] = true
	default:
	}
}

// WaitIdle blocks until queued hashes have been stored or ctx ends.
func (q *MetadataQueue) WaitIdle(ctx context.Context) error {
	if q == nil {
		return nil
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		q.mu.Lock()
		n := len(q.pending)
		q.mu.Unlock()
		if n == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func synchronousArtworkWait(ctx context.Context) (time.Duration, bool) {
	deadline, ok := frontend.SynchronousArtworkDeadline(ctx)
	if !ok {
		return 0, false
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, false
	}
	if remaining > artworkInflightWait {
		remaining = artworkInflightWait
	}
	return remaining, true
}

func (q *MetadataQueue) run() {
	defer q.workers.Done()
	for {
		select {
		case <-q.ctx.Done():
			return
		case job := <-q.jobs:
			ctx, cancel := context.WithTimeout(q.ctx, 30*time.Second)
			var metadata artworkMetadata
			var err, loadErr error
			persist := q.persistable(ctx, job.request)
			if persist {
				err = q.compute(ctx, job)
				metadata, loadErr = q.load(ctx, job.key)
				if err == nil {
					err = loadErr
				}
			} else {
				metadata, err = q.hash(ctx, job)
				loadErr = err
			}
			cancel()
			q.mu.Lock()
			if loadErr == nil && !persist {
				q.memory.Add(job.key, metadata)
			}
			delete(q.pending, job.key)
			if err != nil && q.ctx.Err() == nil {
				if len(q.retry) >= 1024 {
					for key, until := range q.retry {
						if !time.Now().Before(until) {
							delete(q.retry, key)
						}
					}
				}
				if len(q.retry) < 1024 {
					q.retry[job.key] = time.Now().Add(time.Minute)
				}
			} else {
				delete(q.retry, job.key)
			}
			if loadErr == nil && persist {
				if len(q.cache) >= 4096 {
					q.cache = map[string]artworkMetadata{}
				}
				q.cache[job.key] = metadata
			}
			q.mu.Unlock()
		}
	}
}

func (q *MetadataQueue) load(ctx context.Context, key string) (artworkMetadata, error) {
	var m artworkMetadata
	err := q.db.QueryRowContext(ctx, `SELECT width,height,blurhash,thumbhash,used_at FROM artwork_image_metadata WHERE image_key=?`, key).Scan(&m.width, &m.height, &m.blurhash, &m.thumbhash, &m.usedAt)
	return m, err
}

func (q *MetadataQueue) compute(ctx context.Context, job artworkMetadataJob) error {
	viewer, err := q.active(ctx, job.request.Viewer.ID)
	if err != nil || viewer == nil || viewer.ID != job.request.Viewer.ID {
		return core.ErrUnauthorized
	}
	job.request.Viewer = viewer
	if metadata, err := q.load(ctx, job.key); err == nil && metadata.thumbhash != "" {
		return nil
	} else if err != nil && err != sql.ErrNoRows {
		return err
	}
	// A shared lease prevents two nodes warming the same variant concurrently.
	// It outlives the bounded job timeout and recovers after a crashed worker.
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(nonce[:])
	now := time.Now().Unix()
	claim, err := q.db.ExecContext(ctx, `INSERT INTO artwork_image_metadata_claims(image_key,token,expires_at) VALUES(?,?,?) ON CONFLICT(image_key) DO UPDATE SET token=excluded.token,expires_at=excluded.expires_at WHERE artwork_image_metadata_claims.expires_at<?`, job.key, token, now+60, now)
	if err != nil {
		return err
	}
	claimed, err := claim.RowsAffected()
	if err != nil {
		return err
	}
	if claimed == 0 {
		return core.ErrNotAvailable
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = q.db.ExecContext(cleanup, `DELETE FROM artwork_image_metadata_claims WHERE image_key=? AND token=?`, job.key, token)
	}()
	if metadata, err := q.load(ctx, job.key); err == nil && metadata.thumbhash != "" {
		return nil
	} else if err != nil && err != sql.ErrNoRows {
		return err
	}
	m, err := q.hashImage(ctx, job)
	if err != nil {
		return err
	}
	_, err = q.db.ExecContext(ctx, `INSERT INTO artwork_image_metadata(image_key,width,height,blurhash,thumbhash,used_at) VALUES(?,?,?,?,?,?) ON CONFLICT(image_key) DO UPDATE SET thumbhash=excluded.thumbhash,used_at=excluded.used_at WHERE artwork_image_metadata.thumbhash=''`, job.key, m.width, m.height, m.blurhash, m.thumbhash, time.Now().Unix())
	return err
}

// hash computes an unpersisted item's metadata: the same viewer check and
// image read, no lease and no write.
func (q *MetadataQueue) hash(ctx context.Context, job artworkMetadataJob) (artworkMetadata, error) {
	viewer, err := q.active(ctx, job.request.Viewer.ID)
	if err != nil || viewer == nil || viewer.ID != job.request.Viewer.ID {
		return artworkMetadata{}, core.ErrUnauthorized
	}
	job.request.Viewer = viewer
	return q.hashImage(ctx, job)
}

func (q *MetadataQueue) hashImage(ctx context.Context, job artworkMetadataJob) (artworkMetadata, error) {
	data, _, err := q.read(ctx, job.request, job.image, frontend.ArtworkTransform{})
	if err != nil {
		return artworkMetadata{}, err
	}
	if len(data) == 0 || len(data) > 15<<20 {
		return artworkMetadata{}, core.ErrNotAvailable
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 4000 || cfg.Height > 4000 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		return artworkMetadata{}, core.ErrNotAvailable
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return artworkMetadata{}, err
	}
	hash, err := artwork.BlurHash(decoded, 4, 3)
	if err != nil {
		return artworkMetadata{}, err
	}
	thumb, err := artwork.ThumbHash(decoded)
	if err != nil {
		return artworkMetadata{}, err
	}
	return artworkMetadata{width: cfg.Width, height: cfg.Height, blurhash: hash, thumbhash: base64.StdEncoding.EncodeToString(thumb)}, nil
}

// Wait joins workers after the owning context is cancelled.
func (q *MetadataQueue) Wait() { q.workers.Wait() }
