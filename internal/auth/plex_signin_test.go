package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plextv"
)

func TestAuthenticatePlexAccount(t *testing.T) {
	s, v, c := setup(t)
	ctx := context.Background()
	v.mu.Lock()
	v.accounts["dave-device"] = plextv.Account{ID: 102, Username: "dave"}
	v.mu.Unlock()

	// Alice has a library restriction from SyncFriends; set a rating cap too so
	// we verify full viewer policy comes back intact.
	aliceInitial, err := s.Authenticate(ctx, "alice-access")
	if err != nil {
		t.Fatal(err)
	}
	aliceInitial.MaxContentRating = "PG-13"
	aliceInitial.StreamLimit = 2
	if err := s.UpdateUser(ctx, *aliceInitial); err != nil {
		t.Fatal(err)
	}

	usersBefore, err := s.AllUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Owner's plex.tv account token resolves to the owner.
	owner, plexID, err := s.AuthenticatePlexAccount(ctx, "owner-device")
	if err != nil || owner == nil || !owner.IsOwner || plexID != 42 {
		t.Fatalf("owner: user=%+v plexID=%d err=%v", owner, plexID, err)
	}
	if linkID := s.PlexLinkID(ctx, owner.ID, plexID); linkID == nil || *linkID <= 0 {
		t.Fatalf("missing owner plex identity_link: %v", linkID)
	}

	// 2. Shared friend and Plex Home member resolve with their restrictions.
	alice, plexID, err := s.AuthenticatePlexAccount(ctx, "alice-device")
	if err != nil || alice == nil || alice.IsOwner || plexID != 100 || alice.MaxContentRating != "PG-13" || alice.StreamLimit != 2 || len(alice.AllowedLibraries) != 1 || alice.AllowedLibraries[0] != 1 {
		t.Fatalf("alice: user=%+v plexID=%d err=%v", alice, plexID, err)
	}
	kid, plexID, err := s.AuthenticatePlexAccount(ctx, "home-user")
	if err != nil || kid == nil || kid.IsOwner || plexID != 300 || kid.Title != "Kid" {
		t.Fatalf("home user: user=%+v plexID=%d err=%v", kid, plexID, err)
	}

	// 3. Unknown token -> ErrUnauthorized, no viewer created.
	countAfterHome, _ := s.AllUsers(ctx)
	if u, id, err := s.AuthenticatePlexAccount(ctx, "unknown-plex-token"); !errors.Is(err, core.ErrUnauthorized) || u != nil || id != 0 {
		t.Fatalf("unknown token: user=%+v id=%d err=%v", u, id, err)
	}

	// 4. Valid plex.tv account without server access -> ErrPlexNotShared (including cached repeat), no viewer created.
	for attempt := 0; attempt < 2; attempt++ {
		u, id, err := s.AuthenticatePlexAccount(ctx, "stranger")
		if !errors.Is(err, ErrPlexNotShared) || u != nil || id != 999 {
			t.Fatalf("stranger attempt %d: user=%+v id=%d err=%v", attempt, u, id, err)
		}
	}
	// Pending friend's personal plex.tv account token -> ErrPlexNotShared.
	if u, id, err := s.AuthenticatePlexAccount(ctx, "dave-device"); !errors.Is(err, ErrPlexNotShared) || u != nil || id != 102 {
		t.Fatalf("pending friend: user=%+v id=%d err=%v", u, id, err)
	}
	afterRejections, _ := s.AllUsers(ctx)
	if len(afterRejections) != len(countAfterHome) || len(countAfterHome) != len(usersBefore)+1 {
		t.Fatalf("unexpected viewer creation: before=%d afterHome=%d afterRejections=%d", len(usersBefore), len(countAfterHome), len(afterRejections))
	}

	// 5. Server token, delegation token, owner console session and Ether grant are rejected.
	delTok, err := s.IssueDelegation(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessTok, _, err := s.IssueOwnerSession(ctx, "server-token")
	if err != nil {
		t.Fatal(err)
	}
	callsBefore := v.calls.Load()
	for name, bad := range map[string]string{
		"empty":        "   ",
		"server-token": "server-token",
		"delegation":   delTok,
		"owner-sess":   sessTok,
		"ether-grant":  core.GrantCredentialPrefix + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	} {
		if u, id, err := s.AuthenticatePlexAccount(ctx, bad); !errors.Is(err, core.ErrUnauthorized) || u != nil || id != 0 {
			t.Fatalf("%s accepted: user=%+v id=%d err=%v", name, u, id, err)
		}
	}
	if v.calls.Load() != callsBefore {
		t.Fatalf("non-account credentials reached plex.tv validator")
	}
	// Meanwhile Service.Accept still accepts the server token for Plex callers.
	if u, _, err := s.Accept(ctx, "server-token"); err != nil || u == nil || !u.IsOwner {
		t.Fatalf("Service.Accept broke for server-token: %+v %v", u, err)
	}

	// 6. Sharing the server with stranger clears the negative cache on SyncFriends.
	if err := s.SyncFriends(ctx, []plextv.Friend{
		{Owned: true, Token: "owner-access", PlexID: 42},
		{PlexID: 100, Username: "alice", Title: "Alice", Token: "alice-access", Sections: []string{"1"}},
		{PlexID: 999, Username: "stranger", Title: "Stranger", AllLibs: true},
	}); err != nil {
		t.Fatal(err)
	}
	if u, id, err := s.AuthenticatePlexAccount(ctx, "stranger"); err != nil || u == nil || id != 999 {
		t.Fatalf("newly shared friend rejected: user=%+v id=%d err=%v", u, id, err)
	}

	// 7. plex.tv outage -> ErrPlexUnreachable for uncached token, stale allowed within StaleTTL.
	c.add(11 * time.Minute)
	v.setDown(true)
	if u, id, err := s.AuthenticatePlexAccount(ctx, "owner-device"); err != nil || u == nil || !u.IsOwner || id != 42 {
		t.Fatalf("stale owner failed during outage: user=%+v id=%d err=%v", u, id, err)
	}
	if u, id, err := s.AuthenticatePlexAccount(ctx, "uncached-during-outage"); !errors.Is(err, ErrPlexUnreachable) || u != nil || id != 0 {
		t.Fatalf("uncached outage: user=%+v id=%d err=%v", u, id, err)
	}
}

func TestActivePlexUserByPlexID(t *testing.T) {
	s, _, _ := setup(t)
	if u := s.ActivePlexUserByPlexID(42); u == nil || !u.IsOwner {
		t.Fatalf("owner lookup = %+v", u)
	}
	if u := s.ActivePlexUserByPlexID(100); u == nil || u.Username != "alice" {
		t.Fatalf("alice lookup = %+v", u)
	}
	// Pending friend (102), unknown accounts and non-ids have no active viewer.
	for _, id := range []int64{102, 999, 0, -1} {
		if u := s.ActivePlexUserByPlexID(id); u != nil {
			t.Fatalf("lookup %d = %+v, want nil", id, u)
		}
	}
	if got := s.OwnerPlexID(); got != 42 {
		t.Fatalf("OwnerPlexID = %d, want 42", got)
	}
	ctx := context.Background()
	if err := s.SyncFriends(ctx, []plextv.Friend{{PlexID: 100, Username: "alice", Token: "alice-access", Sections: []string{"1"}}}); err != nil {
		t.Fatal(err)
	}
	if u := s.ActivePlexUserByPlexID(101); u != nil {
		t.Fatalf("deactivated bob should not be active: %+v", u)
	}
	known, err := s.KnownUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var foundBob bool
	for _, u := range known {
		if u.PlexID == 101 && u.Username == "bob" {
			foundBob = true
		}
	}
	if !foundBob {
		t.Fatalf("KnownUsers missing deactivated bob: %+v", known)
	}
}

func TestAuthenticatePlexAccountRejectsUnconfiguredServer(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_users(id,plex_id,username,is_owner,active,origin,created_at,updated_at) VALUES(77,NULL,'local',1,1,'local',1,1)`); err != nil {
		t.Fatal(err)
	}
	v := &acceptingValidator{}
	s, err := New(ctx, db, Options{Validator: v})
	if err != nil {
		t.Fatal(err)
	}
	u, id, err := s.AuthenticatePlexAccount(ctx, "any-plex-token")
	if !errors.Is(err, core.ErrUnauthorized) || u != nil || id != 0 {
		t.Fatalf("unconfigured server accepted token: user=%+v id=%d err=%v", u, id, err)
	}
	if v.calls != 0 {
		t.Fatalf("unconfigured server contacted plex.tv %d times", v.calls)
	}
}
