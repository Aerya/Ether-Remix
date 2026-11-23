package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestViewerLifecycleRefusalsAndRemoval(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	s, err := New(ctx, db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(username, q string) int64 {
		t.Helper()
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := db.QueryRowContext(ctx, `SELECT id FROM auth_users WHERE username=?`, username).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	owner := mk("owner", `INSERT INTO auth_users(username,title,is_owner,active,origin,created_at,updated_at) VALUES('owner','Owner',1,1,'local',1,1)`)
	local := mk("bob", `INSERT INTO auth_users(username,title,is_owner,active,origin,created_at,updated_at) VALUES('bob','Bob',0,1,'local',1,1)`)
	other := mk("amy", `INSERT INTO auth_users(username,title,is_owner,active,origin,created_at,updated_at) VALUES('amy','Amy',0,1,'local',1,1)`)
	if err := s.SyncFriends(ctx, []plextv.Friend{
		{PlexID: 42, Username: "friend", Token: "tok-friend"},
		{PlexID: 43, Username: "homekid", Token: "tok-home", Home: true},
	}); err != nil {
		t.Fatal(err)
	}
	var plex, plexHome int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_users WHERE plex_id=42`).Scan(&plex); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_users WHERE plex_id=43`).Scan(&plexHome); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}

	// The owner refuses pause, resume, remove and merge. Plex viewers allow
	// pause and resume, while still refusing remove and merge.
	if err := s.PauseViewer(ctx, owner); !errors.Is(err, ErrViewerIsOwner) {
		t.Fatalf("pause owner: %v, want %v", err, ErrViewerIsOwner)
	}
	if err := s.ResumeViewer(ctx, owner); !errors.Is(err, ErrViewerIsOwner) {
		t.Fatalf("resume owner: %v, want %v", err, ErrViewerIsOwner)
	}
	for _, tc := range []struct {
		name string
		id   int64
		want error
	}{
		{"owner", owner, ErrViewerIsOwner},
		{"plex", plex, ErrViewerPlexManaged},
		{"plexHome", plexHome, ErrViewerPlexManaged},
	} {
		if err := s.RemoveViewer(ctx, tc.id); !errors.Is(err, tc.want) {
			t.Fatalf("remove %s: %v, want %v", tc.name, err, tc.want)
		}
		if err := s.MergeViewers(ctx, tc.id, local); !errors.Is(err, tc.want) {
			t.Fatalf("merge from %s: %v, want %v", tc.name, err, tc.want)
		}
		if err := s.MergeViewers(ctx, local, tc.id); !errors.Is(err, tc.want) {
			t.Fatalf("merge into %s: %v, want %v", tc.name, err, tc.want)
		}
	}
	if err := s.MergeViewers(ctx, plex, plexHome); !errors.Is(err, ErrViewerPlexManaged) {
		t.Fatalf("merge plex into plexHome: %v", err)
	}
	if err := s.MergeViewers(ctx, local, local); !errors.Is(err, ErrMergeSameViewer) {
		t.Fatalf("merge self: %v", err)
	}
	if err := s.RemoveViewer(ctx, 99999); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("remove missing: %v", err)
	}
	if err := s.PauseViewer(ctx, 99999); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("pause missing: %v", err)
	}
	if err := s.ResumeViewer(ctx, 99999); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("resume missing: %v", err)
	}

	// Pause flips owner_enabled and blocks the user; resume restores it.
	if err := s.PauseViewer(ctx, local); err != nil {
		t.Fatal(err)
	}
	infos, err := s.AllUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]UserInfo{}
	for _, info := range infos {
		byID[info.ID] = info
	}
	if byID[local].OwnerEnabled || byID[local].Active {
		t.Fatalf("paused viewer still enabled: %+v", byID[local])
	}
	if !byID[plex].HasPlex || !byID[plex].PlexShared || byID[plexHome].HasPlex != true {
		t.Fatalf("plex flags wrong: %+v %+v", byID[plex], byID[plexHome])
	}
	if err := s.ResumeViewer(ctx, local); err != nil {
		t.Fatal(err)
	}

	// Removal deletes the row and the viewer's identity links and tokens.
	if _, err := db.ExecContext(ctx, `INSERT INTO identity_links(viewer_id,provider,subject,display,verified_at,password_hash) VALUES(?,'local','bob','bob',1,'hash')`, local); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_sessions(token_hash,user_id,created_at,expires_at) VALUES('sess',?,1,9999999999)`, local); err != nil {
		t.Fatal(err)
	}
	n, err := s.RevokeViewerSessions(ctx, local)
	if err != nil || n != 1 {
		t.Fatalf("revoke sessions n=%d err=%v", n, err)
	}
	if err := s.RemoveViewer(ctx, local); err != nil {
		t.Fatal(err)
	}
	var count int
	for _, q := range []string{
		`SELECT COUNT(*) FROM auth_users WHERE id=?`,
		`SELECT COUNT(*) FROM identity_links WHERE viewer_id=?`,
	} {
		if err := db.QueryRowContext(ctx, q, local).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", q, count, err)
		}
	}
	if _, err := s.User(ctx, local); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("removed viewer still in cache: %v", err)
	}

	// Merge of two local viewers works at this level too.
	if _, err := db.ExecContext(ctx, `INSERT INTO identity_links(viewer_id,provider,subject,display,verified_at,password_hash) VALUES(?,'local','amy','amy',1,'hash')`, other); err != nil {
		t.Fatal(err)
	}
	third := mk("cat", `INSERT INTO auth_users(username,title,is_owner,active,origin,created_at,updated_at) VALUES('cat','Cat',0,1,'local',1,1)`)
	if err := s.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.MergeViewers(ctx, other, third); err != nil {
		t.Fatal(err)
	}
	var linkViewer int64
	if err := db.QueryRowContext(ctx, `SELECT viewer_id FROM identity_links WHERE subject='amy'`).Scan(&linkViewer); err != nil || linkViewer != third {
		t.Fatalf("merged link viewer=%d err=%v, want %d", linkViewer, err, third)
	}
	if _, err := s.User(ctx, other); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("merged-away viewer still present: %v", err)
	}
}

func TestEnsurePlexFriend(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	s, err := New(ctx, db, Options{})
	if err != nil {
		t.Fatal(err)
	}

	uid, err := s.EnsurePlexFriend(ctx, plextv.SharedUser{
		PlexID:   703916194,
		UUID:     "uuid-sakib",
		Username: "sakib99",
		Title:    "Sakib Talukder",
		Email:    "sakib@example.com",
		Thumb:    "https://plex.tv/users/sakib/avatar",
	})
	if err != nil || uid <= 0 {
		t.Fatalf("EnsurePlexFriend failed: uid=%d err=%v", uid, err)
	}

	var (
		plexID     int64
		uuid       string
		username   string
		title      string
		email      string
		thumb      string
		origin     string
		active     int
		plexShared int
		linkSubj   string
	)
	if err := db.QueryRowContext(ctx, `SELECT plex_id, uuid, username, title, email, thumb, origin, active, plex_shared FROM auth_users WHERE id=?`, uid).
		Scan(&plexID, &uuid, &username, &title, &email, &thumb, &origin, &active, &plexShared); err != nil {
		t.Fatal(err)
	}
	if plexID != 703916194 || uuid != "uuid-sakib" || username != "sakib99" || title != "Sakib Talukder" || email != "sakib@example.com" || thumb != "https://plex.tv/users/sakib/avatar" || origin != "friend" || active != 0 || plexShared != 0 {
		t.Fatalf("unexpected row: plexID=%d uuid=%q username=%q title=%q email=%q thumb=%q origin=%q active=%d plexShared=%d", plexID, uuid, username, title, email, thumb, origin, active, plexShared)
	}
	if err := db.QueryRowContext(ctx, `SELECT subject FROM identity_links WHERE viewer_id=? AND provider='plex'`, uid).Scan(&linkSubj); err != nil || linkSubj != "703916194" {
		t.Fatalf("unexpected identity_links row: subj=%q err=%v", linkSubj, err)
	}

	// Re-upsert with updated title returns same ID and updates title.
	uid2, err := s.EnsurePlexFriend(ctx, plextv.SharedUser{
		PlexID:   703916194,
		Username: "sakib99",
		Title:    "Sakib T.",
	})
	if err != nil || uid2 != uid {
		t.Fatalf("expected same uid %d on re-upsert, got %d (err=%v)", uid, uid2, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT title FROM auth_users WHERE id=?`, uid).Scan(&title); err != nil || title != "Sakib T." {
		t.Fatalf("expected updated title Sakib T., got %q (err=%v)", title, err)
	}

	// Re-upsert with empty title preserves existing title.
	uid3, err := s.EnsurePlexFriend(ctx, plextv.SharedUser{
		PlexID:   703916194,
		Username: "sakib99",
	})
	if err != nil || uid3 != uid {
		t.Fatalf("expected same uid %d on empty-title re-upsert, got %d (err=%v)", uid, uid3, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT title FROM auth_users WHERE id=?`, uid).Scan(&title); err != nil || title != "Sakib T." {
		t.Fatalf("expected title Sakib T. preserved on empty title, got %q (err=%v)", title, err)
	}

	// EnsurePlexFriend reloads the in-memory cache immediately so User/AllUsers see it.
	if u, err := s.User(ctx, uid); err != nil || u.PlexID != 703916194 || u.Title != "Sakib T." {
		t.Fatalf("expected in-memory cache to immediately contain ensured friend, got u=%+v err=%v", u, err)
	}

	// FriendlyName fallback and preserving is_home/restricted when re-upserted from partial source.
	homeUID, err := s.EnsurePlexFriend(ctx, plextv.SharedUser{
		PlexID:       880011,
		Username:     "kid_home",
		FriendlyName: "Kids Profile",
		Home:         true,
		Restricted:   true,
	})
	if err != nil || homeUID <= 0 {
		t.Fatalf("EnsurePlexFriend home user failed: %v", err)
	}
	if _, err := s.EnsurePlexFriend(ctx, plextv.SharedUser{
		PlexID:   880011,
		Username: "kid_home",
		Email:    "kid@example.com",
	}); err != nil {
		t.Fatalf("re-upsert home user from partial source failed: %v", err)
	}
	var isHome, restricted int
	if err := db.QueryRowContext(ctx, `SELECT title, is_home, restricted FROM auth_users WHERE id=?`, homeUID).Scan(&title, &isHome, &restricted); err != nil {
		t.Fatal(err)
	}
	if title != "Kids Profile" || isHome != 1 || restricted != 1 {
		t.Fatalf("expected FriendlyName title and preserved home/restricted flags, got title=%q is_home=%d restricted=%d", title, isHome, restricted)
	}
}

func TestPlexPauseResumeSyncTokenRefreshAndHome(t *testing.T) {
	s, v, _ := setup(t)
	ctx := context.Background()
	v.mu.Lock()
	v.access["alice-device"] = true
	v.mu.Unlock()

	// Add a Plex Home member via SyncFriends so both friend and Home share paths are tested.
	if err := s.SyncFriends(ctx, []plextv.Friend{
		{Owned: true, Token: "owner-access", PlexID: 42},
		{PlexID: 100, Username: "alice", Title: "Alice", Token: "alice-access", Sections: []string{"1"}},
		{PlexID: 300, Username: "kid", Title: "Kid", Token: "kid-access", Home: true, Restricted: true, AllLibs: true},
	}); err != nil {
		t.Fatal(err)
	}
	aliceID := userByName(t, s, "alice")
	kidID := userByName(t, s, "kid")

	// Mint durable + fresh acceptance receipts before pausing.
	_, aliceDurable, err := s.Accept(ctx, "alice-access")
	if err != nil {
		t.Fatalf("accept alice synced token: %v", err)
	}
	_, aliceFresh, err := s.Accept(ctx, "alice-device")
	if err != nil {
		t.Fatalf("accept alice personal token: %v", err)
	}
	_, kidFresh, err := s.Accept(ctx, "home-user")
	if err != nil {
		t.Fatalf("accept kid home token: %v", err)
	}
	if !s.HomeMember(kidID) {
		t.Fatal("expected kid to be an active home member before pause")
	}

	// Pause both the shared Plex friend and the Plex Home user.
	genBefore := s.Generation()
	if err := s.PauseViewer(ctx, aliceID); err != nil {
		t.Fatalf("pause alice: %v", err)
	}
	if s.Generation() == genBefore {
		t.Fatal("pausing a viewer must bump auth generation")
	}
	if err := s.PauseViewer(ctx, kidID); err != nil {
		t.Fatalf("pause kid: %v", err)
	}

	// Receipts, synced tokens, personal tokens, Home membership and plex.tv sign-in all fail immediately.
	if err := s.ReceiptCurrent(ctx, aliceDurable); err == nil {
		t.Fatal("pre-pause durable receipt survived pause")
	}
	if err := s.ReceiptCurrent(ctx, aliceFresh); err == nil {
		t.Fatal("pre-pause fresh receipt survived pause")
	}
	if err := s.ReceiptCurrent(ctx, kidFresh); err == nil {
		t.Fatal("pre-pause home receipt survived pause")
	}
	if err := s.DurableTokenCurrent(ctx, "alice-access", aliceID); err == nil {
		t.Fatal("DurableTokenCurrent succeeded for paused Plex viewer")
	}
	for _, tok := range []string{"alice-access", "alice-device", "kid-access", "home-user"} {
		if _, _, err := s.Accept(ctx, tok); !errors.Is(err, core.ErrUnauthorized) {
			t.Fatalf("Accept(%q) while paused = %v, want ErrUnauthorized", tok, err)
		}
		if _, _, err := s.AuthenticatePlexAccount(ctx, tok); !errors.Is(err, ErrPlexNotShared) {
			t.Fatalf("AuthenticatePlexAccount(%q) while paused = %v, want ErrPlexNotShared", tok, err)
		}
	}
	if s.ActivePlexUserByPlexID(100) != nil || s.ActivePlexUserByPlexID(300) != nil {
		t.Fatal("ActivePlexUserByPlexID returned a paused Plex viewer")
	}
	if s.HomeMember(kidID) {
		t.Fatal("HomeMember returned true for paused Plex Home viewer")
	}

	// Even during a plex.tv outage, stale fallback must never admit a paused viewer.
	v.setDown(true)
	if _, _, err := s.Accept(ctx, "alice-device"); err == nil {
		t.Fatal("stale fallback admitted paused Plex viewer during outage")
	}
	v.setDown(false)

	// Friends sync (including rotated tokens) and unknown-token refresh must never re-enable paused viewers.
	remoteFriends := []plextv.Friend{
		{Owned: true, Token: "owner-access", PlexID: 42},
		{PlexID: 100, Username: "alice", Title: "Alice", Token: "alice-access-rotated", Sections: []string{"1"}},
		{PlexID: 300, Username: "kid", Title: "Kid", Token: "kid-access-rotated", Home: true, Restricted: true, AllLibs: true},
	}
	s.SetUnknownTokenRefresher(func(ctx context.Context) error {
		return s.SyncFriends(ctx, remoteFriends)
	})
	if err := s.SyncFriends(ctx, remoteFriends); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{"alice-access-rotated", "alice-device", "kid-access-rotated", "home-user"} {
		if _, _, err := s.Accept(ctx, tok); !errors.Is(err, core.ErrUnauthorized) {
			t.Fatalf("Accept(%q) after sync while paused = %v, want ErrUnauthorized", tok, err)
		}
	}
	if s.HomeMember(kidID) {
		t.Fatal("SyncFriends re-enabled paused Plex Home viewer")
	}

	// Resuming a still-shared viewer restores access, while pre-pause durable receipts stay retired.
	if err := s.ResumeViewer(ctx, aliceID); err != nil {
		t.Fatalf("resume alice: %v", err)
	}
	if err := s.ResumeViewer(ctx, kidID); err != nil {
		t.Fatalf("resume kid: %v", err)
	}
	if err := s.ReceiptCurrent(ctx, aliceDurable); err == nil {
		t.Fatal("pre-pause durable receipt resurrected after resume")
	}
	if _, _, err := s.Accept(ctx, "alice-access-rotated"); err != nil {
		t.Fatalf("alice synced token after resume: %v", err)
	}
	if _, _, err := s.Accept(ctx, "alice-device"); err != nil {
		t.Fatalf("alice personal token after resume: %v", err)
	}
	if !s.HomeMember(kidID) {
		t.Fatal("kid HomeMember not restored after resume")
	}

	// Unsharing on plex.tv while paused leaves resume inactive until plex.tv shares again.
	if err := s.PauseViewer(ctx, aliceID); err != nil {
		t.Fatal(err)
	}
	remoteFriends = []plextv.Friend{
		{Owned: true, Token: "owner-access", PlexID: 42},
		{PlexID: 300, Username: "kid", Title: "Kid", Token: "kid-access-rotated", Home: true, Restricted: true, AllLibs: true},
	}
	if err := s.SyncFriends(ctx, remoteFriends); err != nil {
		t.Fatal(err)
	}
	if err := s.ResumeViewer(ctx, aliceID); err != nil {
		t.Fatal(err)
	}
	infos, err := s.AllUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var aliceInfo UserInfo
	for _, info := range infos {
		if info.ID == aliceID {
			aliceInfo = info
		}
	}
	if !aliceInfo.OwnerEnabled || aliceInfo.PlexShared || aliceInfo.Active {
		t.Fatalf("resumed unshared viewer state = %+v, want ownerEnabled=true plexShared=false active=false", aliceInfo)
	}
	if _, _, err := s.Accept(ctx, "alice-device"); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("resumed unshared viewer admitted: %v", err)
	}

	// Pausing an unshared Plex viewer prevents a later plex.tv re-share from re-enabling them until resumed.
	if err := s.PauseViewer(ctx, aliceID); err != nil {
		t.Fatal(err)
	}
	remoteFriends = append(remoteFriends, plextv.Friend{
		PlexID: 100, Username: "alice", Title: "Alice", Token: "alice-access-reshared", Sections: []string{"1"},
	})
	if err := s.SyncFriends(ctx, remoteFriends); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Accept(ctx, "alice-access-reshared"); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("re-shared paused viewer admitted: %v", err)
	}
	if err := s.ResumeViewer(ctx, aliceID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Accept(ctx, "alice-access-reshared"); err != nil {
		t.Fatalf("resumed re-shared viewer rejected: %v", err)
	}
}
