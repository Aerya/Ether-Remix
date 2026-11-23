package plex

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/safety"
	"github.com/braedonsaunders/ether/internal/store"
)

func attachDownloadSafety(t *testing.T, f *downloadFixture) *safety.Service {
	t.Helper()
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc, err := safety.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetFriendsDownloads(func() bool { return f.srv.cfg().Downloads.AllowFriends })
	svc.SetDownloadDefaults(func() core.DownloadAllowance {
		d := f.srv.cfg().Downloads
		return core.DownloadAllowance{
			LimitUnit:   d.LimitUnit,
			LimitAmount: d.LimitAmount,
			LimitPeriod: d.LimitPeriod,
			MaxHeight:   d.MaxHeight,
			NoTranscode: d.NoTranscode,
		}
	})
	f.srv.setTestPolicy(svc)
	return svc
}

func setDownloadConfig(f *downloadFixture, cfg frontend.HostConfig) {
	f.srv.mutateHost(func(h *frontend.Host) {
		h.HostConfig = func() frontend.HostConfig { return cfg }
	})
}

func TestDownloadTranscodingSwitchIndependentOfPlayback(t *testing.T) {
	f := newDownloadFixture(t, true)
	cfg := f.srv.cfg()
	cfg.Downloads.AllowFriends = true
	setDownloadConfig(f, cfg)
	svc := attachDownloadSafety(t, f)

	// Playback VideoTranscodingDisabled=true must NOT disable download conversion.
	if err := svc.Put(context.Background(), friendUser.ID, safety.Policy{VideoTranscodingDisabled: true}); err != nil {
		t.Fatal(err)
	}
	qid := f.queueAs(t, friendToken, "")
	rec := f.do(t, http.MethodPost, "/downloadQueue/"+itoa(qid)+"/add?keys=/library/metadata/"+fightClub.Key.String(), friendToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	f.waitItemStatusAs(t, qid, friendToken, fightClub.Key, downloadAvailable)
	f.remux.mu.Lock()
	if len(f.remux.requests) != 1 || f.remux.requests[0].Caps.NoVideoTranscode {
		f.remux.mu.Unlock()
		t.Fatalf("playback switch leaked into download caps: %+v", f.remux.requests)
	}
	f.remux.mu.Unlock()

	// Per-viewer DownloadTranscodingDisabled=true sets Caps.NoVideoTranscode and shows per-viewer wording on ErrVideoTranscodingDisabled.
	if err := svc.Put(context.Background(), friendUser.ID, safety.Policy{DownloadTranscodingDisabled: true}); err != nil {
		t.Fatal(err)
	}
	f.remux.err = frontend.ErrVideoTranscodingDisabled
	rec = f.do(t, http.MethodPost, "/downloadQueue/"+itoa(qid)+"/add?keys=/library/metadata/"+alien.Key.String(), friendToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("add alien: %d %s", rec.Code, rec.Body.String())
	}
	f.waitItemStatusAs(t, qid, friendToken, alien.Key, downloadError)
	item := f.findItemAs(t, qid, friendToken, alien.Key)
	if got, _ := item["error"].(string); got != frontend.MsgViewerDownloadTranscodingOff {
		t.Fatalf("per-viewer download transcode error = %q, want %q", got, frontend.MsgViewerDownloadTranscodingOff)
	}

	// Server-wide Downloads.NoTranscode shows server-wide download wording even for owner.
	cfg.Downloads.NoTranscode = true
	setDownloadConfig(f, cfg)
	oqid := f.queue(t)
	rec = f.do(t, http.MethodPost, "/downloadQueue/"+itoa(oqid)+"/add?keys=/library/metadata/"+alien.Key.String(), ownerToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner add: %d %s", rec.Code, rec.Body.String())
	}
	f.waitStatus(t, oqid, downloadError)
	items := f.items(t, oqid)
	if got, _ := items[0]["error"].(string); got != frontend.MsgDownloadTranscodingOff {
		t.Fatalf("server-wide download transcode error = %q, want %q", got, frontend.MsgDownloadTranscodingOff)
	}
}

func TestDownloadQueueEnforcesTitleAndGBLimitsWithReconcile(t *testing.T) {
	f := newDownloadFixture(t, true)
	cfg := f.srv.cfg()
	cfg.Downloads.AllowFriends = true
	setDownloadConfig(f, cfg)
	svc := attachDownloadSafety(t, f)

	// 1. Daily title limit of 1: first succeeds, second fails with clear message + reset time.
	if err := svc.Put(context.Background(), friendUser.ID, safety.Policy{
		DownloadLimitUnit:   "titles",
		DownloadLimitAmount: 1,
		DownloadLimitPeriod: "day",
	}); err != nil {
		t.Fatal(err)
	}
	qid := f.queueAs(t, friendToken, "")
	rec := f.do(t, http.MethodPost, "/downloadQueue/"+itoa(qid)+"/add?keys=/library/metadata/"+fightClub.Key.String(), friendToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("add 1: %d %s", rec.Code, rec.Body.String())
	}
	f.waitItemStatusAs(t, qid, friendToken, fightClub.Key, downloadAvailable)

	rec = f.do(t, http.MethodPost, "/downloadQueue/"+itoa(qid)+"/add?keys=/library/metadata/"+alien.Key.String(), friendToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("add 2: %d %s", rec.Code, rec.Body.String())
	}
	item := f.findItemAs(t, qid, friendToken, alien.Key)
	if item["status"] != downloadError {
		t.Fatalf("second title status = %v, want %q", item["status"], downloadError)
	}
	errMsg, _ := item["error"].(string)
	if !strings.Contains(errMsg, "You've reached your download limit for today") || !strings.Contains(errMsg, "resets") {
		t.Fatalf("over-limit error = %q, want clear message with reset time", errMsg)
	}

	// 2. GB limit reconcile catches prepared overshoot BEFORE remux runs and releases reservation.
	if err := svc.Put(context.Background(), friendUser.ID, safety.Policy{
		DownloadLimitUnit:   "gb",
		DownloadLimitAmount: 2,
		DownloadLimitPeriod: "day",
	}); err != nil {
		t.Fatal(err)
	}
	// Make prepared media 3 GB (> 2 GB limit); fakePlayback.Estimate returns empty MediaInfo (0 bytes at admit),
	// so ReconcileDownloadBytes at Prepared must catch the overshoot before RemuxDownload is invoked.
	f.playback.mu.Lock()
	bigInfo := sampleInfo
	bigInfo.Size = 3 << 30
	f.playback.info = bigInfo
	f.playback.mu.Unlock()
	beforeRemux := len(f.remux.requests)

	rec = f.do(t, http.MethodPost, "/downloadQueue/"+itoa(qid)+"/add?keys=/library/metadata/"+bbE1.Key.String(), friendToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("add big episode: %d %s", rec.Code, rec.Body.String())
	}
	f.waitItemStatusAs(t, qid, friendToken, bbE1.Key, downloadError)
	bigItem := f.findItemAs(t, qid, friendToken, bbE1.Key)
	bigMsg, _ := bigItem["error"].(string)
	if !strings.Contains(bigMsg, "download data limit for today") || !strings.Contains(bigMsg, "resets") {
		t.Fatalf("gb overshoot error = %q", bigMsg)
	}
	f.remux.mu.Lock()
	afterRemux := len(f.remux.requests)
	f.remux.mu.Unlock()
	if afterRemux != beforeRemux {
		t.Fatalf("RemuxDownload was called %d times on over-limit prepared item, want 0", afterRemux-beforeRemux)
	}
	// Because the reservation for bbE1 was released, a small 1 GB episode still succeeds.
	f.playback.mu.Lock()
	smallInfo := sampleInfo
	smallInfo.Size = 1 << 30
	f.playback.info = smallInfo
	f.playback.mu.Unlock()
	rec = f.do(t, http.MethodPost, "/downloadQueue/"+itoa(qid)+"/add?keys=/library/metadata/"+bbE2.Key.String(), friendToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("add small episode: %d %s", rec.Code, rec.Body.String())
	}
	f.waitItemStatusAs(t, qid, friendToken, bbE2.Key, downloadAvailable)
}

func TestDirectPartDownloadEnforcesLimits(t *testing.T) {
	f := newDownloadFixture(t, true)
	cfg := f.srv.cfg()
	cfg.Downloads.AllowFriends = true
	setDownloadConfig(f, cfg)
	svc := attachDownloadSafety(t, f)
	if err := svc.Put(context.Background(), friendUser.ID, safety.Policy{
		DownloadLimitUnit:   "titles",
		DownloadLimitAmount: 1,
		DownloadLimitPeriod: "day",
	}); err != nil {
		t.Fatal(err)
	}
	// First direct part download (fightClub) is admitted.
	rec1 := f.do(t, http.MethodGet, "/library/parts/"+itoa(int64(fightClub.Key))+"/1/file.mkv?download=1", friendToken, nil)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first direct download = %d %s", rec1.Code, rec1.Body.String())
	}
	// Second distinct title (alien) hits the daily title limit with 429 and clear reset wording.
	rec2 := f.do(t, http.MethodGet, "/library/parts/"+itoa(int64(alien.Key))+"/1/file.mkv?download=1", friendToken, nil)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second direct download = %d %s, want 429", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), "You've reached your download limit for today") || !strings.Contains(rec2.Body.String(), "resets") {
		t.Fatalf("429 body = %q", rec2.Body.String())
	}
	// Owner is never limited.
	recOwner := f.do(t, http.MethodGet, "/library/parts/"+itoa(int64(alien.Key))+"/1/file.mkv?download=1", ownerToken, nil)
	if recOwner.Code != http.StatusOK {
		t.Fatalf("owner direct download = %d %s, want 200", recOwner.Code, recOwner.Body.String())
	}
}

func (f *downloadFixture) queueAs(t *testing.T, token, clientID string) int64 {
	t.Helper()
	var hdrs map[string]string
	if clientID != "" {
		hdrs = map[string]string{"X-Plex-Client-Identifier": clientID}
	}
	rec := f.do(t, http.MethodPost, "/downloadQueue", token, hdrs)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /downloadQueue = %d %s", rec.Code, rec.Body.String())
	}
	mc := decodeContainer(t, rec)
	dq, _ := mc["DownloadQueue"].([]any)
	return int64(dq[0].(map[string]any)["id"].(float64))
}

func (f *downloadFixture) itemsAs(t *testing.T, qid int64, token string) []map[string]any {
	t.Helper()
	rec := f.do(t, http.MethodGet, "/downloadQueue/"+itoa(qid)+"/items", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("items: %d %s", rec.Code, rec.Body.String())
	}
	mc := decodeContainer(t, rec)
	raw, _ := mc["DownloadQueueItem"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.(map[string]any))
	}
	return out
}

func (f *downloadFixture) findItemAs(t *testing.T, qid int64, token string, key frontend.RatingKey) map[string]any {
	t.Helper()
	wantKey := "/library/metadata/" + key.String()
	for _, it := range f.itemsAs(t, qid, token) {
		if it["key"] == wantKey {
			return it
		}
	}
	t.Fatalf("item %s not found in queue %d", wantKey, qid)
	return nil
}

func (f *downloadFixture) waitItemStatusAs(t *testing.T, qid int64, token string, key frontend.RatingKey, want string) {
	t.Helper()
	wantKey := "/library/metadata/" + key.String()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, it := range f.itemsAs(t, qid, token) {
			if it["key"] == wantKey && it["status"] == want {
				return
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("item %s did not reach status %q; got %+v", wantKey, want, f.itemsAs(t, qid, token))
}
