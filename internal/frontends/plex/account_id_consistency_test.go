package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
	watchstate "github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/stats"
	"github.com/braedonsaunders/ether/internal/store"
)

type lookupAuth struct {
	*fakeAuth
	all []core.User
}

func (a *lookupAuth) User(_ context.Context, id int64) (*core.User, error) {
	for _, u := range a.all {
		if u.ID == id {
			cp := u
			return &cp, nil
		}
	}
	return nil, core.ErrNotFound
}

func (a *lookupAuth) KnownUsers(context.Context) ([]core.User, error) {
	return append([]core.User(nil), a.all...), nil
}

func TestAccountIDConsistentAcrossPlexEndpoints(t *testing.T) {
	// Owner deliberately uses a non-1 local ID alongside a real plex.tv ID to
	// prove canonical ID 1 is emitted everywhere rather than u.ID or u.PlexID.
	owner := core.User{ID: 5, PlexID: 1005, Username: "owner", Title: "Owner", IsOwner: true}
	friend := core.User{ID: 2, PlexID: 2002, Username: "friend", Title: "Friend"}
	homeUser := core.User{ID: 3, PlexID: 3003, Username: "homeuser", Title: "Home User"}
	managed := core.User{ID: 4, PlexID: 4004, Username: "", Title: "Kids"}
	localOnly := core.User{ID: 6, PlexID: 0, Username: "localviewer", Title: "Local Viewer"}
	deactivated := core.User{ID: 7, PlexID: 7007, Username: "former", Title: "Former Friend"}

	tokens := map[string]core.User{
		ownerToken:      owner,
		friendToken:     friend,
		"home-token":    homeUser,
		"managed-token": managed,
		"local-token":   localOnly,
	}
	auth := &lookupAuth{
		fakeAuth: &fakeAuth{users: tokens},
		all:      []core.User{owner, friend, homeUser, managed, localOnly, deactivated},
	}

	f := newFixture(func(d *testServerDeps) {
		setDepsAuth(d, auth)
	})
	defer f.srv.Close()
	subs := newFakeSubtitles()
	subs.offers = []SubtitleOffer{{ID: 42, Language: "en", Title: "English", Provider: "test", Score: 90}}
	f.srv.setSubtitles(subs)

	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := watchstate.New(db, watchstate.Options{Now: func() time.Time { return fixedNow }})
	f.srv.setState(st)
	f.srv.setDashboard(stats.NewDashboard(stats.DashboardOptions{Now: func() time.Time { return fixedNow }, Loc: time.UTC}))

	wantCanonical := map[int64]int64{
		owner.ID:       1,
		friend.ID:      friend.PlexID,
		homeUser.ID:    homeUser.PlexID,
		managed.ID:     managed.PlexID,
		localOnly.ID:   localOnly.ID,
		deactivated.ID: deactivated.PlexID,
	}

	// 1. /accounts lists canonical IDs and non-empty names (Title fallback for managed Home user).
	accountsRec := f.get(t, "/accounts", "Accept", "application/json")
	if accountsRec.Code != http.StatusOK {
		t.Fatalf("/accounts: %d %s", accountsRec.Code, accountsRec.Body.String())
	}
	var accountsDoc struct {
		MC struct {
			Account []struct {
				ID   int64  `json:"id"`
				Key  string `json:"key"`
				Name string `json:"name"`
			} `json:"Account"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(accountsRec.Body.Bytes(), &accountsDoc); err != nil {
		t.Fatalf("decode /accounts: %v\n%s", err, accountsRec.Body.String())
	}
	listed := map[int64]string{}
	for _, a := range accountsDoc.MC.Account {
		if a.Key != "/accounts/"+strconv.FormatInt(a.ID, 10) {
			t.Fatalf("/accounts key mismatch: %+v", a)
		}
		listed[a.ID] = a.Name
	}
	for _, want := range []struct {
		id   int64
		name string
	}{
		{1, "owner"},
		{friend.PlexID, "friend"},
		{homeUser.PlexID, "homeuser"},
		{managed.PlexID, "Kids"},
		{localOnly.ID, "localviewer"},
	} {
		if got, ok := listed[want.id]; !ok || got != want.name {
			t.Fatalf("/accounts[%d] = (%q, %v), want (%q, true); all=%+v", want.id, got, ok, want.name, listed)
		}
	}

	// 2. /accounts/{id} accepts canonical and legacy/alternate IDs, always emits canonical,
	// resolves deactivated accounts, forbids non-owners, and 404s on invalid IDs.
	for _, tc := range []struct {
		queryID int64
		wantID  int64
		wantNm  string
	}{
		{1, 1, "owner"},
		{owner.PlexID, 1, "owner"},
		{owner.ID, 1, "owner"},
		{friend.PlexID, friend.PlexID, "friend"},
		{friend.ID, friend.PlexID, "friend"},
		{managed.PlexID, managed.PlexID, "Kids"},
		{managed.ID, managed.PlexID, "Kids"},
		{localOnly.ID, localOnly.ID, "localviewer"},
		{deactivated.PlexID, deactivated.PlexID, "former"},
		{deactivated.ID, deactivated.PlexID, "former"},
	} {
		rec := f.get(t, fmt.Sprintf("/accounts/%d", tc.queryID), "Accept", "application/json")
		if rec.Code != http.StatusOK {
			t.Fatalf("/accounts/%d: %d %s", tc.queryID, rec.Code, rec.Body.String())
		}
		var one struct {
			MC struct {
				Size    int `json:"size"`
				Account []struct {
					ID   int64  `json:"id"`
					Key  string `json:"key"`
					Name string `json:"name"`
				} `json:"Account"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
			t.Fatalf("decode /accounts/%d: %v", tc.queryID, err)
		}
		if one.MC.Size != 1 || len(one.MC.Account) != 1 ||
			one.MC.Account[0].ID != tc.wantID ||
			one.MC.Account[0].Key != "/accounts/"+strconv.FormatInt(tc.wantID, 10) ||
			one.MC.Account[0].Name != tc.wantNm {
			t.Fatalf("/accounts/%d = %+v, want id=%d name=%q", tc.queryID, one.MC.Account, tc.wantID, tc.wantNm)
		}
	}
	if rec := f.request(t, http.MethodGet, "/accounts/1", map[string]string{"X-Plex-Token": friendToken}); rec.Code != http.StatusForbidden {
		t.Fatalf("friend /accounts/1 = %d, want 403", rec.Code)
	}
	for _, bad := range []string{"/accounts/0", "/accounts/-1", "/accounts/abc", "/accounts/999999"} {
		if rec := f.get(t, bad); rec.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", bad, rec.Code)
		}
	}

	// 3. /status/sessions emits matching User.id and Player.userID for every viewer type.
	for _, tc := range []struct {
		token  string
		client string
	}{
		{ownerToken, "c-owner"},
		{friendToken, "c-friend"},
		{"managed-token", "c-managed"},
		{"local-token", "c-local"},
	} {
		rec := f.request(t, http.MethodGet, "/:/timeline?ratingKey="+fightClub.Key.String()+"&state=playing&time=10000&duration=8340000", map[string]string{
			"X-Plex-Token":              tc.token,
			"X-Plex-Client-Identifier":  tc.client,
			"X-Plex-Session-Identifier": "sess-" + tc.client,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("timeline %s: %d", tc.client, rec.Code)
		}
	}
	sessRec := f.get(t, "/status/sessions", "Accept", "application/json")
	if sessRec.Code != http.StatusOK {
		t.Fatalf("/status/sessions: %d %s", sessRec.Code, sessRec.Body.String())
	}
	var sessDoc struct {
		MC struct {
			Metadata []struct {
				User struct {
					ID string `json:"id"`
				} `json:"User"`
				Player struct {
					MachineID string `json:"machineIdentifier"`
					UserID    int64  `json:"userID"`
				} `json:"Player"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(sessRec.Body.Bytes(), &sessDoc); err != nil {
		t.Fatalf("decode /status/sessions: %v\n%s", err, sessRec.Body.String())
	}
	gotSess := map[string]struct {
		userID   string
		playerID int64
	}{}
	for _, m := range sessDoc.MC.Metadata {
		gotSess[m.Player.MachineID] = struct {
			userID   string
			playerID int64
		}{m.User.ID, m.Player.UserID}
	}
	for client, wantID := range map[string]int64{
		"c-owner":   1,
		"c-friend":  friend.PlexID,
		"c-managed": managed.PlexID,
		"c-local":   localOnly.ID,
	} {
		got, ok := gotSess[client]
		if !ok || got.userID != strconv.FormatInt(wantID, 10) || got.playerID != wantID {
			t.Fatalf("session %s = %+v (ok=%v), want %d", client, got, ok, wantID)
		}
	}

	// 4. History (/status/sessions/history/all, /status/sessions/history/{id}),
	// /library/all/top, and /library/metadata/{key}/users/top emit canonical IDs
	// and accept both canonical and legacy/alternate accountID filters.
	hctx := watchstate.WithHistoryTitle(context.Background(), &fightClub)
	for _, uid := range []int64{owner.ID, friend.ID, managed.ID, localOnly.ID, deactivated.ID} {
		if err := st.Scrobble(hctx, uid, fightClub.Key); err != nil {
			t.Fatal(err)
		}
	}
	allHistRec := f.get(t, "/status/sessions/history/all?type=1", "Accept", "application/json")
	if allHistRec.Code != http.StatusOK {
		t.Fatalf("history/all: %d %s", allHistRec.Code, allHistRec.Body.String())
	}
	var histDoc struct {
		MC struct {
			Metadata []struct {
				HistoryKey string `json:"historyKey"`
				AccountID  int64  `json:"accountID"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(allHistRec.Body.Bytes(), &histDoc); err != nil {
		t.Fatalf("decode history/all: %v", err)
	}
	seenHist := map[int64]string{}
	for _, m := range histDoc.MC.Metadata {
		seenHist[m.AccountID] = m.HistoryKey
	}
	for _, uid := range []int64{owner.ID, friend.ID, managed.ID, localOnly.ID, deactivated.ID} {
		want := wantCanonical[uid]
		hk, ok := seenHist[want]
		if !ok {
			t.Fatalf("history/all missing accountID %d; got %+v", want, seenHist)
		}
		// Single history item must emit the exact same canonical accountID.
		itemRec := f.get(t, hk, "Accept", "application/json")
		if itemRec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", hk, itemRec.Code, itemRec.Body.String())
		}
		var single struct {
			MC struct {
				Metadata []struct {
					AccountID int64 `json:"accountID"`
				} `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(itemRec.Body.Bytes(), &single); err != nil || len(single.MC.Metadata) != 1 || single.MC.Metadata[0].AccountID != want {
			t.Fatalf("%s = %s, want accountID=%d", hk, itemRec.Body.String(), want)
		}
	}
	// Dual-ID filter acceptance on history/all, /library/all/top, /users/top.
	for _, tc := range []struct {
		filter int64
		want   int64
	}{
		{1, 1},
		{owner.PlexID, 1},
		{owner.ID, 1},
		{friend.PlexID, friend.PlexID},
		{friend.ID, friend.PlexID},
		{deactivated.PlexID, deactivated.PlexID},
		{deactivated.ID, deactivated.PlexID},
	} {
		rec := f.get(t, fmt.Sprintf("/status/sessions/history/all?type=1&accountID=%d", tc.filter), "Accept", "application/json")
		var filtered struct {
			MC struct {
				Metadata []struct {
					AccountID int64 `json:"accountID"`
				} `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &filtered); err != nil || len(filtered.MC.Metadata) != 1 || filtered.MC.Metadata[0].AccountID != tc.want {
			t.Fatalf("history/all?accountID=%d -> %s, want 1 row with accountID=%d", tc.filter, rec.Body.String(), tc.want)
		}

		topRec := f.get(t, fmt.Sprintf("/library/all/top?type=1&accountID=%d", tc.filter), "Accept", "application/json")
		var topDoc struct {
			MC struct {
				Metadata []struct {
					User []struct {
						ID int64 `json:"id"`
					} `json:"User"`
				} `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(topRec.Body.Bytes(), &topDoc); err != nil || len(topDoc.MC.Metadata) != 1 || len(topDoc.MC.Metadata[0].User) != 1 || topDoc.MC.Metadata[0].User[0].ID != tc.want {
			t.Fatalf("/library/all/top?accountID=%d -> %s, want User=[{id:%d}]", tc.filter, topRec.Body.String(), tc.want)
		}

		usersTopRec := f.get(t, fmt.Sprintf("/library/metadata/%s/users/top?accountID=%d", fightClub.Key.String(), tc.filter), "Accept", "application/json")
		var usersTopDoc struct {
			MC struct {
				Account []struct {
					ID   int64  `json:"id"`
					Name string `json:"name"`
				} `json:"Account"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(usersTopRec.Body.Bytes(), &usersTopDoc); err != nil || len(usersTopDoc.MC.Account) != 1 || usersTopDoc.MC.Account[0].ID != tc.want || usersTopDoc.MC.Account[0].Name == "" {
			t.Fatalf("/users/top?accountID=%d -> %s, want Account=[{id:%d}]", tc.filter, usersTopRec.Body.String(), tc.want)
		}
	}

	// Non-owner friend can filter own history via canonical or legacy ID,
	// and cannot read owner's history via 1 or owner.PlexID.
	for _, allowed := range []int64{friend.PlexID, friend.ID} {
		rec := f.request(t, http.MethodGet, fmt.Sprintf("/status/sessions/history/all?type=1&accountID=%d", allowed), map[string]string{
			"X-Plex-Token": friendToken, "Accept": "application/json",
		})
		if !strings.Contains(rec.Body.String(), fmt.Sprintf(`"accountID":%d`, friend.PlexID)) {
			t.Fatalf("friend own history filter %d: %s", allowed, rec.Body.String())
		}
	}
	for _, forbidden := range []int64{1, owner.PlexID, owner.ID, deactivated.PlexID} {
		rec := f.request(t, http.MethodGet, fmt.Sprintf("/status/sessions/history/all?type=1&accountID=%d", forbidden), map[string]string{
			"X-Plex-Token": friendToken, "Accept": "application/json",
		})
		if decodeJSON(t, rec)["size"] != float64(0) {
			t.Fatalf("friend cross-account history filter %d leaked: %s", forbidden, rec.Body.String())
		}
	}

	// 5. /statistics/bandwidth and /statistics/media emit canonical IDs and accept both IDs.
	f.srv.dashboard().RecordBytes(owner.ID, owner.Username, "web", "Chrome", "Chrome", true, 4096)
	f.srv.dashboard().RecordBytes(friend.ID, friend.Username, "tv", "Apple TV", "tvOS", false, 2048)
	f.srv.dashboard().RecordBytes(managed.ID, "", "ipad", "iPad", "iOS", true, 1024)
	f.srv.dashboard().RecordBytes(deactivated.ID, deactivated.Username, "old", "Old TV", "tvOS", false, 512)
	f.srv.dashboard().RecordPlay(owner.ID, owner.Username, "web", 1, 3600)
	f.srv.dashboard().RecordPlay(friend.ID, friend.Username, "tv", 1, 1800)

	bwRec := f.get(t, "/statistics/bandwidth?timespan=6", "Accept", "application/json")
	var bwDoc struct {
		MC struct {
			Rows []struct {
				AccountID int64 `json:"accountID"`
			} `json:"StatisticsBandwidth"`
			Account []struct {
				ID   int64  `json:"id"`
				Key  string `json:"key"`
				Name string `json:"name"`
			} `json:"Account"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(bwRec.Body.Bytes(), &bwDoc); err != nil {
		t.Fatalf("decode bandwidth: %v", err)
	}
	bwIDs := map[int64]bool{}
	for _, r := range bwDoc.MC.Rows {
		bwIDs[r.AccountID] = true
	}
	for _, want := range []int64{1, friend.PlexID, managed.PlexID, deactivated.PlexID} {
		if !bwIDs[want] {
			t.Fatalf("bandwidth missing accountID %d; got %+v", want, bwIDs)
		}
	}
	bwNames := map[int64]string{}
	for _, a := range bwDoc.MC.Account {
		if a.Key != "/accounts/"+strconv.FormatInt(a.ID, 10) {
			t.Fatalf("bandwidth account key mismatch: %+v", a)
		}
		bwNames[a.ID] = a.Name
	}
	if bwNames[managed.PlexID] != "Kids" || bwNames[1] != "owner" || bwNames[friend.PlexID] != "friend" || bwNames[deactivated.PlexID] != "former" {
		t.Fatalf("bandwidth account lookup names = %+v", bwNames)
	}
	for _, tc := range []struct {
		filter int64
		want   int64
	}{
		{1, 1}, {owner.PlexID, 1}, {owner.ID, 1},
		{friend.PlexID, friend.PlexID}, {friend.ID, friend.PlexID},
		{deactivated.PlexID, deactivated.PlexID},
	} {
		rec := f.get(t, fmt.Sprintf("/statistics/bandwidth?timespan=6&accountID=%d", tc.filter), "Accept", "application/json")
		var one struct {
			MC struct {
				Rows []struct {
					AccountID int64 `json:"accountID"`
				} `json:"StatisticsBandwidth"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil || len(one.MC.Rows) != 1 || one.MC.Rows[0].AccountID != tc.want {
			t.Fatalf("bandwidth?accountID=%d -> %s, want 1 row with accountID=%d", tc.filter, rec.Body.String(), tc.want)
		}
	}
	for _, filter := range []int64{1, owner.PlexID, friend.PlexID, friend.ID} {
		rec := f.get(t, fmt.Sprintf("/statistics/media?timespan=4&accountID=%d", filter), "Accept", "application/json")
		want := int64(1)
		if filter == friend.PlexID || filter == friend.ID {
			want = friend.PlexID
		}
		if !strings.Contains(rec.Body.String(), fmt.Sprintf(`"accountID":%d`, want)) {
			t.Fatalf("media?accountID=%d -> %s, want accountID=%d", filter, rec.Body.String(), want)
		}
	}

	// 6. Subtitle search Stream userID and activity notifications/registry emit
	// canonical IDs while preserving per-viewer visibility isolation.
	for _, tc := range []struct {
		token  string
		wantID int64
	}{
		{ownerToken, 1},
		{friendToken, friend.PlexID},
	} {
		subRec := f.request(t, http.MethodGet, "/library/metadata/"+fightClub.Key.String()+"/subtitles?language=en", map[string]string{
			"X-Plex-Token": tc.token, "Accept": "application/json",
		})
		if subRec.Code != http.StatusOK || !strings.Contains(subRec.Body.String(), fmt.Sprintf(`"userID":%d`, tc.wantID)) {
			t.Fatalf("subtitle search (%s) = %d %s, want userID=%d", tc.token, subRec.Code, subRec.Body.String(), tc.wantID)
		}
	}

	f.srv.publishNotification(model.NotificationContainer{
		Type: "activity", Size: 1,
		ActivityNotification: []model.ActivityNotification{{
			Event: "started", UUID: "act-owner",
			Activity: &model.Activity{UUID: "act-owner", Type: "subtitle.download", Title: "Downloading subtitles", UserID: 1},
		}},
	}, owner.ID)
	f.srv.publishNotification(model.NotificationContainer{
		Type: "activity", Size: 1,
		ActivityNotification: []model.ActivityNotification{{
			Event: "started", UUID: "act-friend",
			Activity: &model.Activity{UUID: "act-friend", Type: "subtitle.download", Title: "Downloading subtitles", UserID: friend.PlexID},
		}},
	}, friend.ID)

	friendAct := f.request(t, http.MethodGet, "/activities", map[string]string{"X-Plex-Token": friendToken, "Accept": "application/json"})
	var actDoc struct {
		MC struct {
			Activity []struct {
				UUID   string `json:"uuid"`
				UserID int64  `json:"userID"`
			} `json:"Activity"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(friendAct.Body.Bytes(), &actDoc); err != nil {
		t.Fatalf("decode friend /activities: %v", err)
	}
	if len(actDoc.MC.Activity) != 1 || actDoc.MC.Activity[0].UUID != "act-friend" || actDoc.MC.Activity[0].UserID != friend.PlexID {
		t.Fatalf("friend /activities = %+v, want only act-friend with userID=%d", actDoc.MC.Activity, friend.PlexID)
	}
}

func TestDelegatedOwnerEmitsCanonicalID1WithoutOwnerScope(t *testing.T) {
	owner := core.User{ID: 5, PlexID: 1005, Username: "owner", Title: "Owner", IsOwner: true}
	delegatedOwner := core.User{ID: 5, PlexID: 1005, Username: "owner", Title: "Owner", IsOwner: false, Delegated: true}
	friend := core.User{ID: 2, PlexID: 2002, Username: "friend", Title: "Friend"}

	auth := &lookupAuth{
		fakeAuth: &fakeAuth{users: map[string]core.User{
			ownerToken:        owner,
			"delegated-token": delegatedOwner,
			friendToken:       friend,
		}},
		all: []core.User{owner, friend},
	}
	f := newFixture(func(d *testServerDeps) { setDepsAuth(d, auth) })
	defer f.srv.Close()

	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := watchstate.New(db, watchstate.Options{Now: func() time.Time { return fixedNow }})
	f.srv.setState(st)
	hctx := watchstate.WithHistoryTitle(context.Background(), &fightClub)
	if err := st.Scrobble(hctx, owner.ID, fightClub.Key); err != nil {
		t.Fatal(err)
	}
	if err := st.Scrobble(hctx, friend.ID, fightClub.Key); err != nil {
		t.Fatal(err)
	}

	// Delegated playback must still emit User.id="1" and Player.userID=1.
	tl := f.request(t, http.MethodGet, "/:/timeline?ratingKey="+fightClub.Key.String()+"&state=playing&time=5000&duration=8340000", map[string]string{
		"X-Plex-Token":              "delegated-token",
		"X-Plex-Client-Identifier":  "delegated-client",
		"X-Plex-Session-Identifier": "delegated-sess",
	})
	if tl.Code != http.StatusOK {
		t.Fatalf("delegated timeline: %d", tl.Code)
	}
	sessRec := f.get(t, "/status/sessions", "Accept", "application/json")
	if !strings.Contains(sessRec.Body.String(), `"id":"1"`) || !strings.Contains(sessRec.Body.String(), `"userID":1`) {
		t.Fatalf("delegated session did not emit canonical id 1: %s", sessRec.Body.String())
	}

	// Delegated history sees only own rows with canonical accountID=1, accepts
	// both accountID=1 and accountID=owner.PlexID, and cannot read friend's rows.
	for _, path := range []string{
		"/status/sessions/history/all?type=1",
		"/status/sessions/history/all?type=1&accountID=1",
		fmt.Sprintf("/status/sessions/history/all?type=1&accountID=%d", owner.PlexID),
	} {
		rec := f.request(t, http.MethodGet, path, map[string]string{"X-Plex-Token": "delegated-token", "Accept": "application/json"})
		var doc struct {
			MC struct {
				Metadata []struct {
					AccountID int64 `json:"accountID"`
				} `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.MC.Metadata) != 1 || doc.MC.Metadata[0].AccountID != 1 {
			t.Fatalf("delegated %s -> %s, want 1 row with accountID=1", path, rec.Body.String())
		}
	}
	cross := f.request(t, http.MethodGet, fmt.Sprintf("/status/sessions/history/all?type=1&accountID=%d", friend.PlexID), map[string]string{
		"X-Plex-Token": "delegated-token", "Accept": "application/json",
	})
	if decodeJSON(t, cross)["size"] != float64(0) {
		t.Fatalf("delegated token read friend history: %s", cross.Body.String())
	}
}

func TestPlexAccountDirCanonicalWinsOverAlternateCollision(t *testing.T) {
	owner := core.User{ID: 1, PlexID: 1001, Username: "owner", IsOwner: true}
	localColliding := core.User{ID: 1001, PlexID: 0, Username: "local1001"}

	f := newFixture(func(d *testServerDeps) {
		setDepsAuth(d, &fakeAuth{users: map[string]core.User{
			ownerToken:    owner,
			"local-token": localColliding,
		}})
	})
	defer f.srv.Close()

	dir := f.srv.newPlexAccountDir(context.Background(), &owner, true)
	got1, ok := dir.resolve(context.Background(), 1)
	if !ok || got1.user.ID != owner.ID || got1.canonical != 1 {
		t.Fatalf("resolve(1) = (%+v, %v), want owner", got1, ok)
	}
	got1001, ok := dir.resolve(context.Background(), 1001)
	if !ok || got1001.user.ID != localColliding.ID || got1001.canonical != 1001 {
		t.Fatalf("resolve(1001) = (%+v, %v), want canonical local1001 over owner alternate PlexID", got1001, ok)
	}
}
