package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/store"
)

type cutoverMembers struct{ db *sql.DB }

func (m cutoverMembers) ActiveUser(ctx context.Context, id int64) (*core.Viewer, error) {
	var active int
	if err := m.db.QueryRowContext(ctx, `SELECT active FROM auth_users WHERE id=?`, id).Scan(&active); err != nil || active != 1 {
		return nil, core.ErrUnauthorized
	}
	return &core.Viewer{ID: id}, nil
}

type cutoverImageBytes struct{}

func (cutoverImageBytes) Get(context.Context, string, int) ([]byte, string, error) {
	return []byte("image"), "image/jpeg", nil
}

func newCutoverImageService(t *testing.T, db *sql.DB, surface frontend.Protocol, now func() time.Time) (*ImageGrantService, string) {
	t.Helper()
	grants, err := NewGrants(db, cutoverMembers{db}, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := grants.Issue(context.Background(), 1, nil, string(surface), core.Device{ID: "tv"})
	if err != nil {
		t.Fatal(err)
	}
	img := frontend.Image{Kind: frontend.ImagePoster, Path: "opaque-poster", Tag: "v1", Subject: "tmdb:1"}
	exp := frontendtest.Experience{ItemFunc: func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
		return frontend.ItemDetail{ImagesAuthoritative: true, Images: []frontend.Image{img}}, nil
	}}
	svc, err := NewImageGrantService(db, surface, grants, func() frontend.Experience { return exp }, cutoverImageBytes{}, time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetImageKey([]byte("cutover-test-image-key"))
	return svc, credential
}

func TestStatelessImageGrantsRetireDatabaseTables(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,1,0,0)`); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations WHERE component='frontend_image_grants'`).Scan(&version); err != nil || version != 8 {
		t.Fatalf("frontend_image_grants version=%d err=%v", version, err)
	}
	if _, err := db.Exec(`SELECT 1 FROM frontend_image_grants`); err == nil {
		t.Fatal("retired frontend_image_grants table still exists")
	}
	service, credential := newCutoverImageService(t, db, "jellyfin", nil)
	token, err := service.Issue(ctx, frontend.ImageGrantRequest{
		Credential: credential,
		Item:       core.MovieKey(1),
		Image:      frontend.Image{Kind: frontend.ImagePoster, Path: "opaque-poster", Tag: "v1", Subject: "tmdb:1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var headerCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM capability_headers WHERE namespace='image'`).Scan(&headerCount); err != nil || headerCount != 0 {
		t.Fatalf("capability_headers image rows=%d err=%v", headerCount, err)
	}
	w := httptest.NewRecorder()
	service.Serve(w, httptest.NewRequest("GET", "/", nil), token)
	if w.Code != 200 || w.Body.String() != "image" {
		t.Fatal(w.Code, w.Body.String())
	}
}

// Reconstruct pre-v8 image grant state and pre-v3 ticket state, then migrate.
// Migration 8 drops frontend_image_grants and purges image capability headers
// while ticket cutover continues cleanly.
func TestCapabilityExistingStateCutover(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,1,0,0)`); err != nil {
		t.Fatal(err)
	}
	service, credential := newCutoverImageService(t, db, "jellyfin", nil)
	token, err := service.Issue(ctx, frontend.ImageGrantRequest{
		Credential: credential,
		Item:       core.MovieKey(1),
		Image:      frontend.Image{Kind: frontend.ImagePoster, Path: "opaque-poster", Tag: "v1", Subject: "tmdb:1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ticket_admitted_insert", "ticket_immutable", "ticket_header_immutable"} {
		table := "capability_tickets"
		if name == "ticket_header_immutable" {
			table = "capability_headers"
		}
		query := "DROP TRIGGER " + name
		if store.DialectOf(db) == store.Postgres {
			query += " ON " + table
		}
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
		if store.DialectOf(db) == store.Postgres {
			if _, err = db.Exec("DROP FUNCTION " + name + "_fn()"); err != nil {
				t.Fatal(err)
			}
		}
	}
	source, _ := grantHash(credential)
	statements := []string{
		`CREATE TABLE frontend_image_grants (hash TEXT PRIMARY KEY, source_hash TEXT NOT NULL REFERENCES identity_grants(hash) ON DELETE CASCADE, viewer_id BIGINT NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE, surface TEXT NOT NULL, item BIGINT NOT NULL, device TEXT NOT NULL, image_kind TEXT NOT NULL, image_path TEXT NOT NULL, image_tag TEXT NOT NULL, expires BIGINT NOT NULL, revoked INTEGER NOT NULL DEFAULT 0, channel TEXT NOT NULL DEFAULT '', image_subject TEXT NOT NULL DEFAULT '')`,
		`ALTER TABLE capability_tickets ADD COLUMN legacy_mirror INTEGER NOT NULL DEFAULT 0`,
		`CREATE TABLE frontend_tickets(hash TEXT PRIMARY KEY,surface TEXT NOT NULL,purpose TEXT NOT NULL,viewer_id BIGINT NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,source_hash TEXT NOT NULL REFERENCES identity_grants(hash) ON DELETE CASCADE,device TEXT NOT NULL,expires BIGINT NOT NULL)`,
		`DELETE FROM capability_headers WHERE namespace IN ('image','ticket')`,
		`DELETE FROM schema_migrations WHERE (component='frontend_image_grants' AND version=8) OR (component='frontend_tickets' AND version=3)`,
	}
	for _, query := range statements {
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	imgDigest := fmt.Sprintf("%064x", 42)
	expMillis := time.Now().Add(time.Minute).UnixMilli()
	if _, err = db.Exec(`INSERT INTO capability_headers VALUES('image',?,1,1,'jellyfin','tv',?)`, imgDigest, expMillis); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO frontend_image_grants VALUES(?,?,1,'jellyfin',1,'{"ID":"tv"}','poster','opaque-poster','v1',?,0,'','tmdb:1')`, imgDigest, source, expMillis); err != nil {
		t.Fatal(err)
	}
	ticketToken := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	sum := sha256.Sum256([]byte(ticketToken))
	digest := hex.EncodeToString(sum[:])
	if _, err = db.Exec(`INSERT INTO frontend_tickets VALUES(?,'jellyfin','events-ws',1,?,'{"ID":"tv"}',?)`, digest, source, time.Now().Add(30*time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	stale := fmt.Sprintf("%064x", 99)
	if _, err = db.Exec(`INSERT INTO capability_headers VALUES('ticket',?,1,1,'jellyfin','tv',?)`, stale, time.Now().Add(time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO capability_tickets(namespace,digest,purpose,source_hash,device,legacy_mirror) VALUES('ticket',?,'events-ws',?,'{"ID":"tv"}',1)`, stale, source); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`SELECT 1 FROM frontend_image_grants`); err == nil {
		t.Fatal("frontend_image_grants survived v8 migration")
	}
	var imageHeaders int
	if err = db.QueryRow(`SELECT COUNT(*) FROM capability_headers WHERE namespace='image'`).Scan(&imageHeaders); err != nil || imageHeaders != 0 {
		t.Fatalf("stale image headers survived v8 migration: count=%d err=%v", imageHeaders, err)
	}
	var staleCount int
	if err = db.QueryRow(`SELECT COUNT(*) FROM capability_headers WHERE namespace='ticket' AND digest=?`, stale).Scan(&staleCount); err != nil || staleCount != 0 {
		t.Fatal("consumed mirror revived", staleCount, err)
	}
	w := httptest.NewRecorder()
	service.Serve(w, httptest.NewRequest("GET", "/", nil), token)
	if w.Code != 200 || w.Body.String() != "image" {
		t.Fatal(w.Code, w.Body.String())
	}
	tickets, err := NewTicketService(db, "jellyfin", service.grants, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := tickets.Consume(ctx, ticketToken, "events-ws")
	if err != nil || got.Device.ID != "tv" {
		t.Fatal(fmt.Sprint(got), err)
	}
	if _, err = db.Exec(`INSERT INTO frontend_tickets VALUES('old','jellyfin','events-ws',1,?,'{}',1)`, source); err == nil {
		t.Fatal("old writer survived")
	}
}
