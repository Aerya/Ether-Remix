package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/vault"
)

type variantProbeResolver struct {
	*fakeResolver
	variants   []core.ReleaseVariant
	searchHits atomic.Int64
	grabHits   atomic.Int64
}

func (r *variantProbeResolver) Resolve(ctx context.Context, req core.ResolveRequest) (*core.Resolution, error) {
	r.searchHits.Add(1)
	return r.fakeResolver.Resolve(ctx, req)
}

func (r *variantProbeResolver) Variants(_ context.Context, _ core.ResolveRequest, max int) ([]core.ReleaseVariant, error) {
	out := append([]core.ReleaseVariant(nil), r.variants...)
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out, nil
}

type fixtureMediaSource struct {
	id         string
	name       string
	data       []byte
	onRead     func(ctx context.Context) error
	onClose    func()
	closed     atomic.Bool
	prefetch   atomic.Int64
	readEvents chan struct{}
}

func (s *fixtureMediaSource) ReadAt(p []byte, off int64) (int, error) {
	return s.ReadAtPrefetchContext(context.Background(), p, off)
}

func (s *fixtureMediaSource) ReadAtPrefetchContext(ctx context.Context, p []byte, off int64) (int, error) {
	s.prefetch.Add(1)
	if s.readEvents != nil {
		select {
		case s.readEvents <- struct{}{}:
		default:
		}
	}
	if s.onRead != nil {
		if err := s.onRead(ctx); err != nil {
			return 0, err
		}
	}
	return bytes.NewReader(s.data).ReadAt(p, off)
}

func (s *fixtureMediaSource) Size() int64       { return int64(len(s.data)) }
func (s *fixtureMediaSource) Name() string      { return s.name }
func (s *fixtureMediaSource) ReleaseID() string { return s.id }
func (s *fixtureMediaSource) Close() error {
	if s.closed.CompareAndSwap(false, true) && s.onClose != nil {
		s.onClose()
	}
	return nil
}
func (s *fixtureMediaSource) SourceID() string { return s.id }

func loadMKVFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "transcode", "testdata", "nativeprobe", "hevc_hdr10_eac3_ac3_ass.mkv"))
	if err != nil {
		t.Fatalf("read mkv fixture: %v", err)
	}
	return b
}

func TestVersionProbesUseKnownFactsAndPersistByContentID(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	dir := payloadTempDir(t)

	vs, err := vault.Open(ctx, vault.Options{Dir: t.TempDir(), DB: db})
	if err != nil {
		t.Fatal(err)
	}
	sampleNZB := []byte(`<nzb><file subject="Anime.2024.1080p.MULTi.mkv" poster="p"><groups><group>alt.test</group></groups><segments><segment bytes="1024" number="1">a1@test</segment></segments></file></nzb>`)
	cid := nzb.ContentID(sampleNZB)
	vaultKey, err := vs.Put(ctx, vault.PutRequest{
		NZB:     sampleNZB,
		Release: core.Release{ID: "rel-vault-a", IndexerID: "idx-a", Protocol: core.ProtocolUsenet},
	})
	if err != nil {
		t.Fatal(err)
	}

	probedInfo := core.MediaInfo{
		Container: "mkv",
		Duration:  95 * time.Minute,
		Size:      5 << 30,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "jpn", Default: true},
			{Index: 2, Type: "audio", Codec: "eac3", Channels: 6, Language: "eng"},
		},
	}

	// Store tracks ONLY in the vault for vaultKey2 (never written to release_probes)
	// to prove WithCachedVariantInfo reads vault.TrackStore directly.
	sampleNZB2 := []byte(`<nzb><file subject="Anime2.2024.1080p.MULTi.mkv" poster="p"><groups><group>alt.test</group></groups><segments><segment bytes="1024" number="1">b1@test</segment></segments></file></nzb>`)
	vaultKey2, err := vs.Put(ctx, vault.PutRequest{
		NZB:     sampleNZB2,
		Release: core.Release{ID: "rel-vault-only", IndexerID: "idx-b", Protocol: core.ProtocolUsenet},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vs.(vault.TrackStore).SetTracks(ctx, vaultKey, 0, 0, probedInfo); err != nil {
		t.Fatal(err)
	}
	if err := vs.(vault.TrackStore).SetTracks(ctx, vaultKey2, 0, 0, probedInfo); err != nil {
		t.Fatal(err)
	}

	h := newHarness(t, nil, nil)
	cfg := config.Defaults(dir)
	m1, err := New(Deps{
		Resolver:   h.resolver,
		Engine:     h.engine,
		Transcoder: h.tr,
		Catalog:    h.catalog,
		SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile {
			return config.DefaultQuality().Profiles[1]
		},
		DB:    db,
		Vault: vs,
		DebridFileMeta: func(rel core.Release) (string, int64, bool) {
			if rel.ID == "rel-debrid-unprobed" {
				return "Anime.2024.1080p.BluRay.DUAL.ENG.JPN.DDP5.1-GRP.mkv", 4 << 30, true
			}
			return "", 0, false
		},
	}, cfg, Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer m1.Close()

	// Record a probe under release A; release B from another indexer shares its ContentID.
	m1.RecordReleaseProbe(ctx, core.Release{
		ID:        "rel-indexer-1",
		Protocol:  core.ProtocolUsenet,
		ContentID: cid,
	}, sampleNZB, probedInfo)

	// Re-open manager to prove durable cross-restart preload by ContentID.
	m2, err := New(Deps{
		Resolver:   h.resolver,
		Engine:     h.engine,
		Transcoder: h.tr,
		Catalog:    h.catalog,
		SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile {
			return config.DefaultQuality().Profiles[1]
		},
		DB:    db,
		Vault: vs,
		DebridFileMeta: func(rel core.Release) (string, int64, bool) {
			if rel.ID == "rel-debrid-unprobed" {
				return "Anime.2024.1080p.BluRay.DUAL.ENG.JPN.DDP5.1-GRP.mkv", 4 << 30, true
			}
			return "", 0, false
		},
	}, cfg, Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()

	title := &core.Title{Key: movieKey, Type: core.TypeMovie, Title: "Anime", Language: "ja"}
	otherViewer := &core.User{ID: 77, Username: "guest"}
	variants := []core.ReleaseVariant{
		{
			ID:      "rel-indexer-2",
			Auto:    true,
			Release: core.Release{ID: "rel-indexer-2", Protocol: core.ProtocolUsenet, ContentID: cid},
			Info:    core.MediaInfo{Estimated: true, Streams: []core.StreamInfo{{Index: 1, Type: "audio", Language: "ja"}}},
		},
		{
			ID:      "rel-debrid-unprobed",
			Release: core.Release{ID: "rel-debrid-unprobed", Protocol: core.ProtocolTorrent, InfoHash: "0123456789abcdef0123456789abcdef01234567"},
			Info:    core.MediaInfo{Estimated: true, Streams: []core.StreamInfo{{Index: 1, Type: "audio", Codec: "eac3", Language: "ja"}, {Index: 2, Type: "audio", Codec: "eac3", Language: ""}}},
		},
		{
			ID:      "vault-" + string(vaultKey2),
			Release: core.Release{ID: "vault-" + string(vaultKey2), GUID: "vault:" + string(vaultKey2), IndexerID: vault.IndexerID, Protocol: core.ProtocolUsenet},
			Info:    core.MediaInfo{Estimated: true, Streams: []core.StreamInfo{{Index: 1, Type: "audio", Language: "ja"}}},
		},
	}

	got := m2.WithCachedVariantInfo(ctx, otherViewer, title, variants)
	if got[0].Info.Estimated {
		t.Fatal("variant matching durable ContentID probe stayed estimated")
	}
	if got[2].Info.Estimated {
		t.Fatal("variant matching vault TrackStore stayed estimated")
	}
	var langs0 []string
	for _, st := range got[0].Info.Streams {
		if st.Type == "audio" {
			langs0 = append(langs0, st.Language)
		}
	}
	if len(langs0) != 2 || langs0[0] != "jpn" || langs0[1] != "eng" {
		t.Fatalf("cross-viewer ContentID variant audio = %v, want [jpn eng]", langs0)
	}

	// Unprobed debrid variant picks up concrete languages from already-fetched file path.
	if !got[1].Info.Estimated {
		t.Fatal("debrid filename metadata should remain estimated until container-probed")
	}
	var langs1 []string
	for _, st := range got[1].Info.Streams {
		if st.Type == "audio" {
			langs1 = append(langs1, st.Language)
		}
	}
	hasEn := false
	for _, l := range langs1 {
		if l == "en" {
			hasEn = true
		}
	}
	if !hasEn {
		t.Fatalf("debrid file metadata did not surface English track: %v", langs1)
	}
}

func TestDetailVersionHeaderProbeFreeBoundedAndYieldsToPlayback(t *testing.T) {
	ctx := context.Background()
	mkvBytes := loadMKVFixture(t)
	db := store.OpenTest(t)
	dir := payloadTempDir(t)

	vaultRel := core.Release{
		ID:        "rel-vaulted",
		Protocol:  core.ProtocolUsenet,
		ContentID: "nzb:1111111111111111111111111111111111111111111111111111111111111111",
		Name:      "Anime.2024.1080p.MULTi-GRP",
	}
	debridRel := core.Release{
		ID:       "rel-debrid-cached",
		Protocol: core.ProtocolTorrent,
		InfoHash: "abcdefabcdefabcdefabcdefabcdefabcdefabcd",
		Name:     "Anime.2024.1080p.Dual-Audio-GRP",
	}
	unvaultedRel := core.Release{
		ID:       "rel-unvaulted-usenet",
		Protocol: core.ProtocolUsenet,
		Name:     "Anime.2024.1080p.MULTi-OTHER",
	}

	h := newHarness(t, nil, nil)
	res := &variantProbeResolver{
		fakeResolver: h.resolver,
		variants: []core.ReleaseVariant{
			{ID: vaultRel.ID, Auto: true, Release: vaultRel, Info: core.MediaInfo{Estimated: true}},
			{ID: debridRel.ID, Release: debridRel, Info: core.MediaInfo{Estimated: true}},
			{ID: unvaultedRel.ID, Release: unvaultedRel, Info: core.MediaInfo{Estimated: true}},
		},
	}

	var (
		mu             sync.Mutex
		inFlight       int
		maxConcurrent  int
		probedReleases []string
	)
	probedCh := make(chan string, 4)

	openTracked := func(id string) core.MediaSource {
		mu.Lock()
		inFlight++
		if inFlight > maxConcurrent {
			maxConcurrent = inFlight
		}
		mu.Unlock()
		return &fixtureMediaSource{
			id:   id,
			name: id + ".mkv",
			data: mkvBytes,
			onClose: func() {
				mu.Lock()
				inFlight--
				mu.Unlock()
			},
		}
	}

	cfg := config.Defaults(dir)
	m, err := New(Deps{
		Resolver:   res,
		Engine:     h.engine,
		Transcoder: h.tr,
		Catalog:    h.catalog,
		SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile {
			return config.DefaultQuality().Profiles[1]
		},
		DB: db,
		FetchNZB: func(context.Context, core.Release) ([]byte, error) {
			res.grabHits.Add(1)
			t.Error("background version probe must never grab an NZB")
			return nil, core.ErrNotAvailable
		},
		OpenVaulted: func(_ context.Context, rel core.Release) (core.MediaSource, []byte, error) {
			if rel.ID != vaultRel.ID {
				return nil, nil, nil // unvaulted Usenet release skipped with zero grab
			}
			src := openTracked(rel.ID)
			return src, nil, nil
		},
		OpenCachedDebrid: func(_ context.Context, rel core.Release) (core.MediaSource, error) {
			if rel.ID != debridRel.ID {
				return nil, nil
			}
			return openTracked(rel.ID), nil
		},
		OnVersionProbed: func(_ core.RatingKey, rel core.Release) {
			mu.Lock()
			probedReleases = append(probedReleases, rel.ID)
			mu.Unlock()
			probedCh <- rel.ID
		},
	}, cfg, Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// Browse prewarm must NOT trigger version header probes.
	browseCaps := defaultCaps("browse-client")
	m.Prewarm(ctx, owner, movieKey, browseCaps, core.PrewarmOptions{Reason: core.PrewarmBrowse})
	m.WaitWarmProbe(ctx, owner, browseCaps, movieKey, 2*time.Second)
	m.versionProbeWG.Wait()
	if _, hit := m.CachedReleaseProbe(vaultRel); hit {
		t.Fatal("browse prewarm triggered version header probe")
	}
	if _, hit := m.CachedReleaseProbe(debridRel); hit {
		t.Fatal("browse prewarm triggered debrid version header probe")
	}

	// Reset search counter after the browse prewarm completes so we verify the
	// viewer-triggered version header probe itself performs zero indexer searches.
	res.searchHits.Store(0)

	// Viewer-triggered detail schedule probes only vaulted Usenet and cached debrid.
	m.scheduleVersionProbes(owner, movieKey, defaultCaps("detail-client"))

	seen := map[string]bool{}
	deadline := time.After(5 * time.Second)
	for len(seen) < 2 {
		select {
		case id := <-probedCh:
			seen[id] = true
		case <-deadline:
			t.Fatalf("timed out waiting for version probes; got %v", seen)
		}
	}
	if !seen[vaultRel.ID] || !seen[debridRel.ID] {
		t.Fatalf("probed = %v, want vaulted and cached debrid releases", seen)
	}
	if res.grabHits.Load() != 0 {
		t.Fatalf("background probe performed %d NZB grabs", res.grabHits.Load())
	}
	if res.searchHits.Load() != 0 {
		t.Fatalf("background probe performed %d indexer searches", res.searchHits.Load())
	}
	if maxConcurrent > 1 {
		t.Fatalf("max concurrent version probes = %d, want 1", maxConcurrent)
	}

	// CachedReleaseProbe now serves real probed tracks for both releases.
	for _, rel := range []core.Release{vaultRel, debridRel} {
		info, hit := m.CachedReleaseProbe(rel)
		if !hit || info.Estimated || info.Video() == nil {
			t.Fatalf("CachedReleaseProbe(%s) hit=%v info=%+v", rel.ID, hit, info)
		}
	}
	if _, hit := m.CachedReleaseProbe(unvaultedRel); hit {
		t.Fatal("unvaulted Usenet release was unexpectedly probed")
	}
}

func TestVersionHeaderProbeYieldsImmediatelyToActivePlayback(t *testing.T) {
	mkvBytes := loadMKVFixture(t)
	db := store.OpenTest(t)
	dir := payloadTempDir(t)

	slowRel := core.Release{
		ID:       "rel-slow-debrid",
		Protocol: core.ProtocolTorrent,
		InfoHash: "1111222233334444555566667777888899990000",
	}
	h := newHarness(t, nil, nil)
	res := &variantProbeResolver{
		fakeResolver: h.resolver,
		variants: []core.ReleaseVariant{
			{ID: slowRel.ID, Auto: true, Release: slowRel, Info: core.MediaInfo{Estimated: true}},
		},
	}

	readStarted := make(chan struct{}, 1)
	readCanceled := make(chan struct{}, 1)
	src := &fixtureMediaSource{
		id:         slowRel.ID,
		name:       "slow.mkv",
		data:       mkvBytes,
		readEvents: readStarted,
		onRead: func(ctx context.Context) error {
			<-ctx.Done()
			select {
			case readCanceled <- struct{}{}:
			default:
			}
			return ctx.Err()
		},
	}

	m, err := New(Deps{
		Resolver:   res,
		Engine:     h.engine,
		Transcoder: h.tr,
		Catalog:    h.catalog,
		SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile {
			return config.DefaultQuality().Profiles[1]
		},
		DB: db,
		OpenCachedDebrid: func(context.Context, core.Release) (core.MediaSource, error) {
			return src, nil
		},
	}, config.Defaults(dir), Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	m.scheduleVersionProbes(owner, movieKey, defaultCaps("detail-client"))
	select {
	case <-readStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("version probe did not start reading")
	}

	// Activating playback must immediately cancel the in-flight version header probe.
	m.mu.Lock()
	m.pendingN++
	m.mu.Unlock()
	m.cancelVersionProbes()

	select {
	case <-readCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("version probe did not yield when playback started")
	}
	m.versionProbeWG.Wait()
	if !src.closed.Load() {
		t.Fatal("yielded version probe did not close its source")
	}
	if _, hit := m.CachedReleaseProbe(slowRel); hit {
		t.Fatal("canceled probe must not store partial facts")
	}
}

func TestCachedItemProbePersistsPluginChaptersAcrossRestart(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil, nil)
	db := store.OpenTest(t)
	dir := t.TempDir()
	cfg := config.Defaults(dir)

	bookKey := core.PluginKey(42001)
	probed := core.MediaInfo{
		Container: "mp3",
		Size:      161_032_192,
		Duration:  20_120 * time.Second,
		Streams:   []core.StreamInfo{{Index: 0, Type: "audio", Codec: "mp3", Channels: 2}},
		Chapters: []core.Chapter{
			{ID: 0, Title: "Opening Credits", Start: 0, End: 16 * time.Second},
			{ID: 1, Title: "Epigraph", Start: 16 * time.Second, End: 44 * time.Second},
			{ID: 2, Title: "Introduction: My Story", Start: 44 * time.Second, End: 1057 * time.Second},
		},
	}

	deps := Deps{
		Resolver:   h.resolver,
		Engine:     h.engine,
		Transcoder: h.tr,
		Catalog:    h.catalog,
		SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile {
			return config.DefaultQuality().Profiles[1]
		},
		DB: db,
	}
	m1, err := New(deps, cfg, Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	m1.persistItemProbe(ctx, bookKey, probed)
	got1, ok := m1.CachedItemProbe(bookKey)
	if !ok || len(got1.Chapters) != 3 || got1.Chapters[2].Title != "Introduction: My Story" {
		t.Fatalf("m1.CachedItemProbe = (%+v, %v), want 3 chapters", got1, ok)
	}
	m1.Close()

	// Fresh Manager simulating restart must preload chapters from release_probes.
	m2, err := New(deps, cfg, Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()

	got2, ok := m2.CachedItemProbe(bookKey)
	if !ok || got2.Estimated || len(got2.Chapters) != 3 || got2.Chapters[0].Title != "Opening Credits" || got2.Chapters[2].Start != 44*time.Second {
		t.Fatalf("m2.CachedItemProbe after restart = (%+v, %v), want 3 persisted chapters", got2, ok)
	}
}

func TestProbeVersionHeadersProbesFreeSourceNonAutoVersions(t *testing.T) {
	ctx := context.Background()
	mkvBytes := loadMKVFixture(t)
	h := newHarness(t, nil, nil)
	db := store.OpenTest(t)
	dir := t.TempDir()
	cfg := config.Defaults(dir)

	autoRel := core.Release{
		ID: "rel-auto", Name: "The.Signal.1999.1080p.WEB-DL.H264-GRP",
		Protocol: core.ProtocolTorrent, InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Size: int64(len(mkvBytes)),
	}
	rel4K := core.Release{
		ID: "rel-4k", Name: "The.Signal.1999.2160p.WEB-DL.HEVC-GRP",
		Protocol: core.ProtocolTorrent, InfoHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Size: int64(len(mkvBytes)),
	}
	vr := &variantProbeResolver{
		fakeResolver: h.resolver,
		variants: []core.ReleaseVariant{
			{ID: core.KindAutoVersionID, Label: "Auto", Auto: true, Release: autoRel, Info: core.MediaInfo{Estimated: true}},
			{ID: core.VersionSelectorPrefix + "eyJrIjoidGllciIsImgiOjIxNjB9", Label: "4K", Release: rel4K, Info: core.MediaInfo{Estimated: true}},
		},
	}

	var opened []string
	var openMu sync.Mutex
	m, err := New(Deps{
		Resolver:   vr,
		Engine:     h.engine,
		Transcoder: h.tr,
		Catalog:    h.catalog,
		SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile {
			return config.DefaultQuality().Profiles[1]
		},
		DB: db,
		OpenCachedDebrid: func(_ context.Context, rel core.Release) (core.MediaSource, error) {
			openMu.Lock()
			opened = append(opened, rel.ID)
			openMu.Unlock()
			return &fixtureMediaSource{id: rel.ID, name: rel.Name + ".mkv", data: mkvBytes}, nil
		},
	}, cfg, Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// Pre-populate Auto's probe so only the non-Auto 4K version still needs probing.
	m.RecordReleaseProbe(ctx, autoRel, nil, core.MediaInfo{
		Container: "mkv", Size: autoRel.Size, Duration: time.Minute,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "aac", Language: "en", Channels: 2},
		},
	})

	if err := m.ProbeVersionHeaders(ctx, owner, movieKey, defaultCaps("plezy-hdr")); err != nil {
		t.Fatalf("ProbeVersionHeaders: %v", err)
	}
	if vr.searchHits.Load() != 0 {
		t.Fatalf("ProbeVersionHeaders invoked Resolve (%d times)", vr.searchHits.Load())
	}
	openMu.Lock()
	gotOpened := append([]string(nil), opened...)
	openMu.Unlock()
	if len(gotOpened) != 1 || gotOpened[0] != "rel-4k" {
		t.Fatalf("opened releases = %v, want only unprobed [rel-4k]", gotOpened)
	}

	enriched := m.WithCachedVariantInfo(ctx, owner, h.catalog.titles[movieKey], vr.variants)
	if len(enriched) != 2 || enriched[1].Info.Estimated || len(enriched[1].Info.Streams) < 3 {
		t.Fatalf("4K variant not enriched with probed streams: %+v", enriched)
	}
	hasSub := false
	for _, st := range enriched[1].Info.Streams {
		if st.Type == "subtitle" {
			hasSub = true
		}
	}
	if !hasSub {
		t.Fatalf("4K probed streams missing subtitle track: %+v", enriched[1].Info.Streams)
	}
}

type streamingSlotWatcherResolver struct {
	*fakeResolver
	emitFn func(ctx context.Context, emit func([]core.ReleaseVariant)) error
}

func (r *streamingSlotWatcherResolver) WatchVersionSlots(
	ctx context.Context,
	_ core.ResolveRequest,
	_ int,
	_ <-chan struct{},
	emit func([]core.ReleaseVariant),
) error {
	return r.emitFn(ctx, emit)
}

// TestProbeVersionHeadersStartsAsSlotsFillAndRetriesOnCacheConfirmation verifies:
//   - A non-Auto free-source version probe starts and completes while the
//     indexer stream is still blocked on slower indexers.
//   - Auto (v.Auto == true) is skipped so concurrent Auto probing is never
//     duplicated.
//   - A torrent that first arrives with unknown cache status (OpenCachedDebrid
//     misses) is retried once debrid earlyCacheChecks re-emits it as cached.
//   - Unvaulted Usenet releases do not trigger NZB grabs.
func TestProbeVersionHeadersStartsAsSlotsFillAndRetriesOnCacheConfirmation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mkvBytes := loadMKVFixture(t)
	h := newHarness(t, nil, nil)
	db := store.OpenTest(t)
	dir := t.TempDir()
	cfg := config.Defaults(dir)

	autoRel := core.Release{
		ID: "rel-auto-live", Name: "The.Signal.1999.1080p.WEB-DL.H264-GRP",
		Protocol: core.ProtocolTorrent, InfoHash: "1111111111111111111111111111111111111111",
		Size: int64(len(mkvBytes)),
	}
	rel4K := core.Release{
		ID: "rel-4k-live", Name: "The.Signal.1999.2160p.WEB-DL.HEVC-GRP",
		Protocol: core.ProtocolTorrent, InfoHash: "2222222222222222222222222222222222222222",
		Size: int64(len(mkvBytes)),
	}
	relLateCache := core.Release{
		ID: "rel-720-late", Name: "The.Signal.1999.720p.WEB-DL.H264-GRP",
		Protocol: core.ProtocolTorrent, InfoHash: "3333333333333333333333333333333333333333",
		Size: int64(len(mkvBytes)),
	}
	relUnvaulted := core.Release{
		ID: "rel-usenet-cold", Name: "The.Signal.1999.1080p.BluRay.x264-COLD",
		Protocol: core.ProtocolUsenet, Size: 5 << 30,
	}

	var lateCached atomic.Bool
	probed4K := make(chan struct{}, 1)
	firstLateAttempt := make(chan struct{}, 1)
	probedLate := make(chan struct{}, 1)
	releaseSearch := make(chan struct{})
	var autoOpened atomic.Int32
	var vaultOpens atomic.Int32

	wr := &streamingSlotWatcherResolver{
		fakeResolver: h.resolver,
		emitFn: func(wctx context.Context, emit func([]core.ReleaseVariant)) error {
			// Batch 1: Auto, cached 4K, not-yet-confirmed 720p torrent, unvaulted Usenet.
			emit([]core.ReleaseVariant{
				{ID: core.KindAutoVersionID, Label: "Auto", Auto: true, Release: autoRel, Availability: core.VersionCached, Info: core.MediaInfo{Estimated: true}},
				{ID: core.VersionSelectorPrefix + "4k", Label: "4K", Release: rel4K, Availability: core.VersionCached, Info: core.MediaInfo{Estimated: true}},
				{ID: core.VersionSelectorPrefix + "720", Label: "720p", Release: relLateCache, Availability: core.VersionFetch, Info: core.MediaInfo{Estimated: true}},
				{ID: core.VersionSelectorPrefix + "usenet", Label: "1080p", Release: relUnvaulted, Availability: core.VersionFetch, Info: core.MediaInfo{Estimated: true}},
			})
			// Wait until 4K has actually been probed AND 720p's initial uncached
			// check ran, while the search stream is still open.
			select {
			case <-probed4K:
			case <-wctx.Done():
				return wctx.Err()
			}
			select {
			case <-firstLateAttempt:
			case <-wctx.Done():
				return wctx.Err()
			}
			// Batch 2: debrid earlyCacheChecks confirms 720p is cached.
			lateCached.Store(true)
			emit([]core.ReleaseVariant{
				{ID: core.KindAutoVersionID, Label: "Auto", Auto: true, Release: autoRel, Availability: core.VersionCached, Info: core.MediaInfo{Estimated: true}},
				{ID: core.VersionSelectorPrefix + "4k", Label: "4K", Release: rel4K, Availability: core.VersionCached, Info: core.MediaInfo{Estimated: true}},
				{ID: core.VersionSelectorPrefix + "720", Label: "720p", Release: relLateCache, Availability: core.VersionCached, Info: core.MediaInfo{Estimated: true}},
				{ID: core.VersionSelectorPrefix + "usenet", Label: "1080p", Release: relUnvaulted, Availability: core.VersionFetch, Info: core.MediaInfo{Estimated: true}},
			})
			select {
			case <-releaseSearch:
				return nil
			case <-wctx.Done():
				return wctx.Err()
			}
		},
	}

	m, err := New(Deps{
		Resolver:   wr,
		Engine:     h.engine,
		Transcoder: h.tr,
		Catalog:    h.catalog,
		SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile {
			return config.DefaultQuality().Profiles[1]
		},
		DB: db,
		OpenCachedDebrid: func(_ context.Context, rel core.Release) (core.MediaSource, error) {
			switch rel.ID {
			case autoRel.ID:
				autoOpened.Add(1)
				return nil, core.ErrNotFound
			case relLateCache.ID:
				if !lateCached.Load() {
					select {
					case firstLateAttempt <- struct{}{}:
					default:
					}
					return nil, core.ErrNotFound
				}
				return &fixtureMediaSource{id: rel.ID, name: rel.Name + ".mkv", data: mkvBytes}, nil
			case rel4K.ID:
				return &fixtureMediaSource{id: rel.ID, name: rel.Name + ".mkv", data: mkvBytes}, nil
			default:
				return nil, core.ErrNotFound
			}
		},
		OpenVaulted: func(_ context.Context, _ core.Release) (core.MediaSource, []byte, error) {
			vaultOpens.Add(1)
			return nil, nil, core.ErrNotFound
		},
		OnVersionProbed: func(_ core.RatingKey, rel core.Release) {
			if rel.ID == rel4K.ID {
				select {
				case probed4K <- struct{}{}:
				default:
				}
			}
			if rel.ID == relLateCache.ID {
				select {
				case probedLate <- struct{}{}:
				default:
				}
			}
		},
	}, cfg, Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	done := make(chan error, 1)
	go func() {
		done <- m.ProbeVersionHeaders(ctx, owner, movieKey, defaultCaps("plezy-live"))
	}()

	// Both 4K and the late-confirmed 720p must finish probing BEFORE the
	// search stream closes.
	select {
	case <-probedLate:
	case <-time.After(3 * time.Second):
		t.Fatal("late cache-confirmed 720p torrent was not probed before search completed")
	}
	close(releaseSearch)

	if err := <-done; err != nil {
		t.Fatalf("ProbeVersionHeaders: %v", err)
	}
	if autoOpened.Load() != 0 {
		t.Fatalf("ProbeVersionHeaders duplicated Auto open (%d times)", autoOpened.Load())
	}
	if vaultOpens.Load() != 1 {
		t.Fatalf("unvaulted Usenet release OpenVaulted calls = %d, want 1 (never retried)", vaultOpens.Load())
	}
	if _, ok := m.CachedReleaseProbe(rel4K); !ok {
		t.Fatal("4K probe not persisted")
	}
	if _, ok := m.CachedReleaseProbe(relLateCache); !ok {
		t.Fatal("late-confirmed 720p probe not persisted")
	}
}

func TestCachedProbeByKeysPersistenceAndPackFileIsolation(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	dir := t.TempDir()
	h := newHarness(t, nil, nil)
	cfg := config.Defaults(dir)

	// Two releases sharing the same pack InfoHash but targeting different
	// FileIndex values must produce distinct ScopedContentID keys.
	idx0, idx1 := 0, 1
	rel0 := core.Release{
		ID: "pack-rel", Protocol: core.ProtocolTorrent,
		InfoHash: "0123456789abcdef0123456789abcdef01234567",
		Target:   &core.ReleaseTarget{FileIndex: &idx0},
	}
	rel1 := core.Release{
		ID: "pack-rel", Protocol: core.ProtocolTorrent,
		InfoHash: "0123456789abcdef0123456789abcdef01234567",
		Target:   &core.ReleaseTarget{FileIndex: &idx1},
	}
	keys0 := releaseProbeContentKeys(rel0, nil, nil)
	keys1 := releaseProbeContentKeys(rel1, nil, nil)
	if len(keys0) == 0 || len(keys1) == 0 || keys0[0] == keys1[0] {
		t.Fatalf("pack file releaseProbeContentKeys collided: keys0=%v keys1=%v", keys0, keys1)
	}
	k0, k1 := keys0[0], keys1[0]

	deps := Deps{
		Resolver:   h.resolver,
		Engine:     h.engine,
		Transcoder: h.tr,
		Catalog:    h.catalog,
		SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile {
			return config.DefaultQuality().Profiles[1]
		},
		DB: db,
	}
	m1, err := New(deps, cfg, Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	m1.RecordProbeByKeys(ctx, []string{k0}, core.MediaInfo{
		Container: "mkv", Size: 1000, Duration: 90 * time.Second,
		Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "av1", Width: 1280, Height: 720}},
	})
	m1.RecordProbeByKeys(ctx, []string{k1}, core.MediaInfo{
		Container: "mkv", Size: 2000, Duration: 2400 * time.Second,
		Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "av1", Width: 1920, Height: 1080}},
	})
	m1.Close()

	// Fresh Manager backed by the same SQLite DB must reload both entries cleanly.
	m2, err := New(deps, cfg, Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()

	got0, ok0 := m2.CachedProbeByKeys([]string{k0})
	got1, ok1 := m2.CachedProbeByKeys([]string{k1})
	if !ok0 || !ok1 {
		t.Fatalf("reload failed: ok0=%v ok1=%v", ok0, ok1)
	}
	if got0.Duration != 90*time.Second || got1.Duration != 2400*time.Second {
		t.Fatalf("reloaded durations = %v / %v, want 90s / 2400s", got0.Duration, got1.Duration)
	}
}

func TestPerEpisodePackProbeAndVaultTrackIsolation(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	dir := t.TempDir()
	vs, err := vault.Open(ctx, vault.Options{Dir: t.TempDir(), DB: db})
	if err != nil {
		t.Fatal(err)
	}

	const packHash = "285f4b2600c4702b546ef71f9747862ecbcf96a7"
	const movieHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// Pre-seed legacy shared bare bt:<packHash> rows (as written pre-#714) alongside
	// a valid single-file movie probe under bare bt:<movieHash>.
	legacyPackInfo := core.MediaInfo{
		Container: "mkv", Duration: 51 * time.Minute, Size: 7370527522,
		Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160}},
	}
	movieInfo := core.MediaInfo{
		Container: "mkv", Duration: 120 * time.Minute, Size: 18 << 30,
		Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160}},
	}
	packRec, _ := json.Marshal(probeFactsRecord{FactsVersion: core.ProbeFactsVersion, Info: legacyPackInfo})
	movieRec, _ := json.Marshal(probeFactsRecord{FactsVersion: core.ProbeFactsVersion, Info: movieInfo})
	if _, err := db.ExecContext(ctx, `INSERT INTO release_probes(content_key, info_json, updated_at) VALUES(?, ?, ?), (?, ?, ?)`,
		"bt:"+packHash, string(packRec), time.Now().UnixMilli(),
		"bt:"+movieHash, string(movieRec), time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
	if err := vs.(vault.TrackStore).SetTracks(ctx, vault.TorrentKey(packHash), 0, 0, legacyPackInfo); err != nil {
		t.Fatal(err)
	}

	h := newHarness(t, nil, nil)
	cfg := config.Defaults(dir)
	deps := Deps{
		Resolver:   h.resolver,
		Engine:     h.engine,
		Transcoder: h.tr,
		Catalog:    h.catalog,
		SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile {
			return config.DefaultQuality().Profiles[1]
		},
		DB:    db,
		Vault: vs,
	}
	m1, err := New(deps, cfg, Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	// AIOStreams-style per-episode pack releases (no FullSeason, shared InfoHash).
	ep4 := core.Release{
		ID:       "aio-ep4",
		GUID:     "btih:" + packHash + "/s1e4",
		Protocol: core.ProtocolTorrent,
		InfoHash: packHash,
		Info:     core.ReleaseInfo{Episodes: []int{4}},
		Target:   &core.ReleaseTarget{Season: 1, Episode: 4},
	}
	ep5 := core.Release{
		ID:       "aio-ep5",
		GUID:     "btih:" + packHash + "/s1e5",
		Protocol: core.ProtocolTorrent,
		InfoHash: packHash,
		Info:     core.ReleaseInfo{Episodes: []int{5}},
		Target:   &core.ReleaseTarget{Season: 1, Episode: 5},
	}
	movie := core.Release{
		ID:       "movie-1",
		GUID:     "btih:" + movieHash,
		Protocol: core.ProtocolTorrent,
		InfoHash: movieHash,
		Target:   &core.ReleaseTarget{MovieTitles: []string{"Movie"}, MovieYear: 2026},
	}

	// Old poisoned bare bt:<packHash> and vault_tracks(bt:<packHash>, 0, 0) must be ignored
	// for per-episode pack releases, while valid single-file movie probes still hit.
	if info, ok := m1.CachedReleaseProbe(ep4); ok {
		t.Fatalf("ep4 hit legacy bare pack probe: %+v", info)
	}
	if info, ok := m1.CachedReleaseProbe(ep5); ok {
		t.Fatalf("ep5 hit legacy bare pack probe: %+v", info)
	}
	if info, ok := m1.CachedReleaseProbe(movie); !ok || info.Duration != 120*time.Minute {
		t.Fatalf("valid single-file movie probe lost: ok=%v info=%+v", ok, info)
	}

	// Recording E04's probe must populate E04 only (release_probes + vault_tracks(vk, 1, 4))
	// and never bleed into E05 (including a vaulted pack copy sharing ID "vault-bt:<hash>").
	ep4Info := core.MediaInfo{
		Container: "mkv", Duration: 51 * time.Minute, Size: 7370527522,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160},
			{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "eng", Default: true},
		},
	}
	vaultEp4 := core.Release{
		ID: "vault-bt:" + packHash, GUID: "vault:bt:" + packHash,
		IndexerID: vault.IndexerID, Protocol: core.ProtocolTorrent, InfoHash: packHash,
		Info:   core.ReleaseInfo{Seasons: []int{1}, Episodes: []int{4}},
		Target: &core.ReleaseTarget{Season: 1, Episode: 4},
	}
	vaultEp5 := core.Release{
		ID: "vault-bt:" + packHash, GUID: "vault:bt:" + packHash,
		IndexerID: vault.IndexerID, Protocol: core.ProtocolTorrent, InfoHash: packHash,
		Info:   core.ReleaseInfo{Seasons: []int{1}, Episodes: []int{4}},
		Target: &core.ReleaseTarget{Season: 1, Episode: 5},
	}
	m1.RecordReleaseProbe(ctx, vaultEp4, nil, ep4Info)

	if info, ok := m1.CachedReleaseProbe(ep4); !ok || info.Size != ep4Info.Size {
		t.Fatalf("ep4 missed its own probe: ok=%v info=%+v", ok, info)
	}
	if info, ok := m1.CachedReleaseProbe(ep5); ok {
		t.Fatalf("ep5 leaked ep4 probe: %+v", info)
	}
	if info, ok := m1.CachedReleaseProbe(vaultEp5); ok {
		t.Fatalf("vaulted ep5 sharing rel.ID leaked ep4 probe via releaseProbeKeys/vault_tracks: %+v", info)
	}

	// Explicit file-selector releases sharing rel.ID must also stay isolated in releaseProbeKeys.
	idx0, idx1 := 0, 1
	file0 := core.Release{ID: "pack-rel", Protocol: core.ProtocolTorrent, InfoHash: packHash, Target: &core.ReleaseTarget{FileIndex: &idx0}}
	file1 := core.Release{ID: "pack-rel", Protocol: core.ProtocolTorrent, InfoHash: packHash, Target: &core.ReleaseTarget{FileIndex: &idx1}}
	m1.RecordReleaseProbe(ctx, file0, nil, ep4Info)
	if info, ok := m1.CachedReleaseProbe(file1); ok {
		t.Fatalf("pack file1 sharing rel.ID leaked file0 probe via releaseProbeKeys: %+v", info)
	}
	if vk, _, _ := vaultTrackTarget(file0, nil, nil); vk != "" {
		t.Fatalf("explicit file target should not share vault_tracks(vk, 0, 0), got %q", vk)
	}

	// Already-scoped ContentIDOf (hex TargetReleaseID from resolver.ContentIDForRelease)
	// must not be double-hashed by releaseProbeContentKeys.
	scopedFromResolver := core.TargetReleaseID("nzb:packhash", core.ReleaseTarget{Season: 1, Episode: 4})
	nzbEp4 := core.Release{
		ID:       "nzb-pack-ep4",
		Protocol: core.ProtocolUsenet,
		Target:   &core.ReleaseTarget{Season: 1, Episode: 4},
	}
	nzbKeys := releaseProbeContentKeys(nzbEp4, nil, func(string) string { return scopedFromResolver })
	if len(nzbKeys) == 0 || nzbKeys[0] != scopedFromResolver {
		t.Fatalf("releaseProbeContentKeys double-scoped ContentIDOf: got %v want leading %q", nzbKeys, scopedFromResolver)
	}

	m1.Close()

	// After restart: E04 still hits from DB + vault_tracks, E05 stays cleanly estimated
	// in WithCachedVariantInfo (used by Plex/Plezy, Jellyfin/Emby, Silo, Vela).
	m2, err := New(deps, cfg, Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()

	ep5Title := &core.Title{Key: core.EpisodeKey(99, 1, 5), Type: core.TypeEpisode, Season: 1, Episode: 5}
	ep5Variants := m2.WithCachedVariantInfo(ctx, nil, ep5Title, []core.ReleaseVariant{
		{ID: ep5.ID, Release: ep5, Info: core.MediaInfo{Estimated: true, Size: 6800000000}},
		// Even an untargeted torrent variant on an episode page must not read legacy bare bt:<hash>.
		{ID: "untargeted-torrent", Release: core.Release{ID: "untargeted-torrent", Protocol: core.ProtocolTorrent, InfoHash: packHash}, Info: core.MediaInfo{Estimated: true, Size: 6800000000}},
	})
	for i, v := range ep5Variants {
		if !v.Info.Estimated || v.Info.Size != 6800000000 {
			t.Fatalf("ep5 variant[%d] leaked legacy/ep4 probe: %+v", i, v.Info)
		}
	}
	if _, hit, err := vs.(vault.TrackStore).Tracks(ctx, vault.TorrentKey(packHash), 1, 4); err != nil || !hit {
		t.Fatalf("vault_tracks missing ep4 (1,4) row: hit=%v err=%v", hit, err)
	}
	if _, hit, err := vs.(vault.TrackStore).Tracks(ctx, vault.TorrentKey(packHash), 1, 5); err != nil || hit {
		t.Fatalf("vault_tracks unexpectedly hit ep5 (1,5): hit=%v err=%v", hit, err)
	}
}
