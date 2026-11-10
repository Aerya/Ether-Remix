package identity_test

import (
	"context"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
	"time"
)

func TestLastSurfaceActivityExcludesUnusedRevokedExpiredAndInactive(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO auth_users(id,plex_id,active,created_at,updated_at) VALUES(1,1,1,0,0),(2,2,0,0,0)`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		hash, surface                  string
		viewer, used, expires, revoked int64
	}{
		{"used", "jellyfin", 1, 2000, 9000, 0}, {"unused", "jellyfin", 1, 1000, 9000, 0},
		{"revoked", "jellyfin", 1, 7000, 9000, 1}, {"expired", "jellyfin", 1, 6000, 7000, 0},
		{"inactive", "jellyfin", 2, 7000, 9000, 0}, {"other", "emby", 1, 7000, 9000, 0},
	} {
		_, err := db.Exec(`INSERT INTO identity_grants(hash,viewer_id,surface,device_id,client,product,version,created,last_used,expires,revoked) VALUES(?,?,?,'d','','','',1000,?,?,?)`, tc.hash, tc.viewer, tc.surface, tc.used, tc.expires, tc.revoked)
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := identity.LastSurfaceActivity(ctx, db, "jellyfin", time.UnixMilli(8000))
	if err != nil || len(got) != 1 || got[1] != 2 {
		t.Fatalf("activity=%v err=%v", got, err)
	}
}
