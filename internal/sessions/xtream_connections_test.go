package sessions

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"net/http/httptest"
	"testing"
	"time"
)

func TestXtreamConnectionLimitSharedAcrossNodes(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_users(id,username,title,active,created_at,updated_at,stream_limit) VALUES(1,'viewer','Viewer',1,0,0,3)`); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"grant", "second"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO identity_grants(hash,viewer_id,surface,device_id,client,product,version,created,last_used,expires,xtream_stream_limit) VALUES(?,1,'xtream','tv','','','',0,0,?,?)`, ref, now.Add(time.Hour).UnixMilli(), map[string]int{"grant": 2}[ref]); err != nil {
			t.Fatal(err)
		}
	}
	b := &Broker{deps: BrokerDeps{SurfacePolicy: testSessionPolicy, Now: func() time.Time { return now }}}
	one := &lazyStreams{db: db, broker: b, protocol: "xtream", grants: xtreamGrants{}}
	two := &lazyStreams{db: db, broker: b, protocol: "xtream", grants: xtreamGrants{}}
	r := httptest.NewRequest("GET", "http://ether.test/file", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	admit := func(s *lazyStreams, ref, item string, want bool) {
		t.Helper()
		peer, target, err := xtreamBinding(r, item)
		if err != nil {
			t.Fatal(err)
		}
		err = s.admitXtream(ctx, ref, 1, peer, target, "", xtreamConnectionTTL, true)
		if (err == nil) != want {
			t.Fatalf("%s/%s admission=%v want=%v", ref, item, err, want)
		}
	}
	has := func(ref, item string, want bool) {
		t.Helper()
		_, target, _ := xtreamBinding(r, item)
		var count int
		err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions_xtream_connections WHERE grant_reference=? AND target=? AND expires>?`, ref, target, now.UnixMilli()).Scan(&count)
		if err != nil || (count == 1) != want {
			t.Fatalf("%s/%s present=%d want=%v err=%v", ref, item, count, want, err)
		}
	}
	admit(one, "grant", "movie/1", true)
	now = now.Add(time.Millisecond)
	admit(two, "grant", "movie/2", true)
	has("grant", "movie/1", true) // below the cap: multi-view preserves the first
	now = now.Add(time.Millisecond)
	admit(two, "grant", "movie/1", true) // renewal does not change creation order
	admit(one, "grant", "movie/3", true) // at device cap: newest wins immediately
	has("grant", "movie/1", false)
	has("grant", "movie/2", true)
	has("grant", "movie/3", true)
	// Superseded lazy segments cannot resurrect their removed admission.
	if _, release, err := two.acquireXtreamConnection(r, "grant", 1, "movie/1"); err == nil {
		release()
		t.Fatal("superseded stream renewed")
	}
	admit(two, "second", "movie/4", true) // shared viewer cap is now full
	r.RemoteAddr = "192.0.2.2:1234"
	admit(one, "second", "movie/5", false) // a different peer cannot supersede
	r.RemoteAddr = "192.0.2.1:1234"
	now = now.Add(time.Millisecond)
	admit(one, "second", "movie/5", true) // same peer can switch at viewer cap
	has("second", "movie/4", false)
	has("second", "movie/5", true)
	if limit, err := one.AppConnectionLimit(ctx, "password"); err != nil || limit != 2 {
		t.Fatal("effective min", limit, err)
	}
	if active, err := one.AppConnections(ctx, "password"); err != nil || active != 2 {
		t.Fatal("active count", active, err)
	}
	// With no lease on the requesting login, viewer capacity cannot be stolen.
	if _, err := db.ExecContext(ctx, `DELETE FROM sessions_xtream_connections WHERE grant_reference='second'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_users SET stream_limit=2 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	admit(two, "second", "movie/6", false)
	// At cap one an actively renewed same-peer stream switches immediately.
	if _, err := db.ExecContext(ctx, `DELETE FROM sessions_xtream_connections`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE identity_grants SET xtream_stream_limit=1 WHERE hash='grant'`); err != nil {
		t.Fatal(err)
	}
	admit(one, "grant", "live/one", true)
	admit(two, "grant", "live/two", true)
	has("grant", "live/one", false)
	has("grant", "live/two", true)
	now = now.Add(31 * time.Second)
	admit(two, "second", "movie/6", true)
}

type xtreamGrants struct{}

func (xtreamGrants) GrantReference(context.Context, string, string) (string, *core.Viewer, error) {
	return "grant", &core.Viewer{ID: 1}, nil
}
func (xtreamGrants) ValidateGrantReference(context.Context, string, string, int64) error { return nil }
