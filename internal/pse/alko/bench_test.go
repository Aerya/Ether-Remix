package alko

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/pse/quality"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/store"
)

// Banc de mesure de la lecture complète (issue #209). Il ne tourne que si
// ALKO_BENCH_DB (copie de ether.db, jamais la base réelle) et ALKO_BENCH_DATA
// (lignes JSON du catalogue SFR) sont définies : jamais en CI.
//
//	ALKO_BENCH_DB=/copie/ether.db ALKO_BENCH_DATA=page.jsonl ALKO_BENCH_PAGES=20 \
//	  go test ./internal/pse/alko -run TestBenchFullRead -v -count=1 -cpuprofile cpu.out

// timedPublisher mesure chaque appel au publieur réel.
type timedPublisher struct {
	Publisher
	mu    sync.Mutex
	calls map[string]int
	took  map[string]time.Duration
}

func (p *timedPublisher) add(name string, start time.Time) {
	p.mu.Lock()
	p.calls[name]++
	p.took[name] += time.Since(start)
	p.mu.Unlock()
}

func (p *timedPublisher) Publish(ctx context.Context, f []releasedb.AlkoFile, fr bool) (releasedb.AlkoResult, error) {
	defer p.add("Publish", time.Now())
	return p.Publisher.Publish(ctx, f, fr)
}
func (p *timedPublisher) Refresh(ctx context.Context, k []core.RatingKey) error {
	defer p.add("Refresh", time.Now())
	return p.Publisher.Refresh(ctx, k)
}
func (p *timedPublisher) Forget(ctx context.Context, g []releasedb.AlkoGone) error {
	defer p.add("Forget", time.Now())
	return p.Publisher.Forget(ctx, g)
}

func benchElements(t *testing.T, path string, n int) []Element {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	var out []Element
	for sc.Scan() && len(out) < n {
		line := strings.ReplaceAll(sc.Text(), `\\`, `\`) // double échappement de l'extraction psql
		var e Element
		if json.Unmarshal([]byte(line), &e) != nil || e.PlaybackID == "" {
			continue
		}
		e.SourceURL = "https://alldebrid.com/f/bench" + strconv.Itoa(len(out))
		out = append(out, e)
	}
	return out
}

// benchDigest résume le résultat (hors horodatages) pour comparer deux versions.
func benchDigest(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	qs := map[string]string{
		"pse_alko_files":      `SELECT playback_id,kind,tmdb,season,episode,filename,filesize,fingerprint,synced_at,langs,COALESCE(info_json,''),url_sealed,content_hash,pub,pub_sig,rel_id,title_key FROM pse_alko_files ORDER BY playback_id`,
		"pse_releases":        `SELECT id,title_key,media_type,season,episode,source_name,protocol,name,size,height,video_codec,audio_codec,hdr,dolby_vision,languages,subtitle_languages,debrid_cached,debrid_uncached,cache_until FROM pse_releases WHERE source_id='` + releasedb.AlkoSourceID + `' ORDER BY id`,
		"pse_release_library": `SELECT title_key,release_id,source_signature,payload,browse FROM pse_release_library WHERE source_id='` + releasedb.AlkoSourceID + `' ORDER BY title_key,release_id`,
		"pse_playable":        `SELECT title_key,releases,any_lang FROM pse_playable ORDER BY title_key`,
	}
	out := map[string]string{}
	for name, q := range qs {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		h := sha256.New()
		n := 0
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(h, vals...)
			n++
		}
		rows.Close()
		out[name] = fmt.Sprintf("%d lignes %s", n, hex.EncodeToString(h.Sum(nil))[:16])
	}
	return out
}

func TestBenchFullRead(t *testing.T) {
	dbPath, dataPath := os.Getenv("ALKO_BENCH_DB"), os.Getenv("ALKO_BENCH_DATA")
	if dbPath == "" || dataPath == "" {
		t.Skip("ALKO_BENCH_DB et ALKO_BENCH_DATA non définies")
	}
	pages := 20
	if v, err := strconv.Atoi(os.Getenv("ALKO_BENCH_PAGES")); err == nil && v > 0 {
		pages = v
	}
	ctx := context.Background()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	db, err := store.OpenPath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Réglages PSE réels de la copie (filtre de langue, « lisible »…).
	settings := map[string]string{}
	if rows, err := db.Query(`SELECT k,v FROM pse_settings`); err == nil {
		for rows.Next() {
			var k, v string
			_ = rows.Scan(&k, &v)
			settings[k] = v
		}
		rows.Close()
	}
	if v := os.Getenv("ALKO_BENCH_PLAYABLE"); v != "" {
		settings["PSE_PLAYABLE"] = v // « on » : le recalcul des titres lisibles est actif
	}
	defer pseenv.Replace(settings)()
	langFR := !isOff(settings["PSE_ALKO_LANG_FR"])
	for k, v := range settings {
		if u := strings.ToUpper(k); !strings.Contains(u, "KEY") && !strings.Contains(u, "TOKEN") && !strings.Contains(u, "PASS") && !strings.Contains(u, "SECRET") && !strings.Contains(u, "URL") {
			t.Logf("réglage %s=%.40s", k, v)
		}
	}
	if s, err := quality.New(ctx, db); err != nil {
		t.Logf("profils qualité : %v", err)
	} else {
		defer s.Close()
		c := s.Config()
		t.Logf("profils qualité : mode=%q, %d profils", c.Mode, len(c.Profiles))
	}
	t.Logf("réglages PSE : %d, profils qualité actifs : %v, lisible : %v", len(settings), quality.Active(db), releasedb.Playable().Enabled)
	// État de départ : première lecture complète, rien de publié par la source.
	// ALKO_BENCH_PREPARED=1 : la copie est déjà dans cet état (voir ALKO_BENCH_PREPARE).
	resets := []string{
		`DELETE FROM pse_alko_files`, `DELETE FROM pse_alko_checked`, `DELETE FROM pse_state WHERE k LIKE 'alko_%'`,
		`DELETE FROM pse_releases WHERE source_id='` + releasedb.AlkoSourceID + `'`,
		`DELETE FROM pse_release_library WHERE source_id='` + releasedb.AlkoSourceID + `'`,
	}
	if os.Getenv("ALKO_BENCH_PREPARED") == "" {
		for _, q := range resets {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
	}
	if os.Getenv("ALKO_BENCH_PREPARE") != "" {
		for range 3 {
			_ = store.Checkpoint(ctx, db, false)
		}
		_, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
		t.Logf("copie préparée : %v", err)
		return
	}
	// Comme en production : un point de contrôle PASSIVE par seconde, WAL vidé avant de mesurer.
	cpCtx, stopCP := context.WithCancel(ctx)
	defer stopCP()
	go store.RunCheckpointer(cpCtx, db, nil, nil)
	for range 5 {
		_ = store.Checkpoint(ctx, db, false)
	}
	if ok, err := store.TruncateWAL(ctx, db, time.Minute); err != nil {
		t.Logf("troncature du WAL : %v", err)
	} else {
		t.Logf("WAL tronqué : %v", ok)
	}
	elems := benchElements(t, dataPath, pages*PageSize)
	t.Logf("%d éléments lus", len(elems))
	f := newFakeSFR(t)
	f.pageMax = PageSize
	for _, e := range elems {
		f.files[e.PlaybackID] = e
	}
	zero := time.Duration(0)
	pub := &timedPublisher{calls: map[string]int{}, took: map[string]time.Duration{}}
	var sealTook time.Duration
	sealed := 0
	lib := func() releasedb.Library {
		return releasedb.Library{Accept: func(ctx context.Context, key core.RatingKey, r core.Release) (bool, error) {
			if quality.Active(db) {
				var original string
				_ = db.QueryRowContext(ctx, `SELECT language FROM catalog_titles WHERE key=?`, int64(key)).Scan(&original)
				typ := core.TypeMovie
				if key.Kind() == core.KindEpisode || key.Kind() == core.KindShow {
					typ = core.TypeShow
				}
				return quality.Allow(db, key, typ, r, original), nil
			}
			return true, nil
		}, Store: releasedb.NewStore(db), Signature: "bench", Seal: testSeal, Open: testOpen}
	}()
	pub.Publisher = &releasedb.AlkoPublisher{DB: db, Remember: func(ctx context.Context, key core.RatingKey, rs []core.Release) {
		_ = lib.Save(ctx, key, rs)
	}}
	svc := &Service{
		Settings: Settings{Enabled: true, URL: f.srv.URL, Key: testKey, LangFR: langFR, Every: 10 * time.Minute},
		DB:       db, Pause: &zero,
		Seal: func(s string) (string, error) {
			defer func(t0 time.Time) { sealTook += time.Since(t0); sealed++ }(time.Now())
			return testSeal(s)
		},
		Open: testOpen, Publisher: pub,
	}
	var stamps []time.Time
	f.afterCatalogue = func() { stamps = append(stamps, time.Now()) }
	store.ResetWriteGateHolds()
	if err := svc.checkSource(ctx); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := svc.fullRead(ctx, svc.client()); err != nil {
		t.Fatal(err)
	}
	if err := svc.flushDirty(ctx); err != nil {
		t.Fatal(err)
	}
	total := time.Since(start)
	h := store.ReadWriteGateHolds()
	t.Logf("BENCH total %s pour %d pages : %.2f s/page ; estimation 382 pages : %s", total.Round(time.Millisecond), pages, total.Seconds()/float64(pages), (total * 382 / time.Duration(pages)).Round(time.Second))
	for _, name := range []string{"Publish", "Refresh", "Forget"} {
		t.Logf("BENCH %s : %d appels, %s", name, pub.calls[name], pub.took[name].Round(time.Millisecond))
	}
	t.Logf("BENCH Seal : %d, %s", sealed, sealTook.Round(time.Millisecond))
	t.Logf("BENCH écrivain SQLite : %d tenues, total %s, max %s, <1ms %d <5ms %d <20ms %d <100ms %d <1s %d >1s %d",
		h.Holds, h.Held.Round(time.Millisecond), h.Max.Round(time.Millisecond), h.Buckets[0], h.Buckets[1], h.Buckets[2], h.Buckets[3], h.Buckets[4], h.Buckets[5])
	var gaps []string
	for i := 1; i < len(stamps); i++ {
		gaps = append(gaps, strconv.FormatFloat(stamps[i].Sub(stamps[i-1]).Seconds(), 'f', 1, 64))
	}
	t.Logf("BENCH écarts entre pages (s) : %s", strings.Join(gaps, " "))
	for name, d := range benchDigest(t, db) {
		t.Logf("BENCH digest %s : %s", name, d)
	}
}
