package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type exactRestartPlayback struct {
	*fakeVariantPlayback
	exactCalls []string
}

func (p *exactRestartPlayback) PrepareExactVariant(ctx context.Context, u *core.User, k core.RatingKey, caps core.ClientCaps, releaseID string) (*core.PlaySession, error) {
	p.mu.Lock()
	p.exactCalls = append(p.exactCalls, releaseID)
	p.mu.Unlock()
	return p.PrepareVariant(ctx, u, k, caps, releaseID)
}

// A subtitle change restarts playback (PUT part, stop, new decision). The
// restart is an Auto request whose fresh choice may be another release; it
// must reopen exactly the release that was playing.
func TestTrackChangeRestartKeepsWatchedRelease(t *testing.T) {
	pb := &exactRestartPlayback{fakeVariantPlayback: &fakeVariantPlayback{fakePlayback: newFakePlayback(sampleInfo)}}
	pb.resolution = &core.Resolution{Title: fightClub.Key, Release: core.Release{ID: "rel-watched"}}
	f := newVariantFixture(t, pb)
	decision := "/video/:/transcode/universal/decision?path=/library/metadata/" + fightClub.Key.String() + "&session=s1"
	if rec := f.get(t, decision); rec.Code != http.StatusOK || pb.prepared != 1 {
		t.Fatalf("first decision code=%d prepared=%d: %s", rec.Code, pb.prepared, rec.Body.String())
	}
	// Selection now prefers another release.
	pb.resolution = &core.Resolution{Title: fightClub.Key, Release: core.Release{ID: "rel-other"}}

	put := httptest.NewRequest(http.MethodPut, "/library/parts/"+fightClub.Key.String()+"?subtitleStreamID=0&allParts=1", nil)
	put.RemoteAddr = "192.168.1.50:51000"
	put.Header.Set("X-Plex-Token", ownerToken)
	put.Header.Set("X-Plex-Client-Identifier", "client-1")
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, put)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status %d: %s", rec.Code, rec.Body.String())
	}
	for _, ps := range pb.Sessions() {
		pb.Stop(ps.ID)
	}

	if rec := f.get(t, decision); rec.Code != http.StatusOK {
		t.Fatalf("restart decision status %d: %s", rec.Code, rec.Body.String())
	}
	if len(pb.exactCalls) != 1 || pb.exactCalls[0] != "rel-watched" || pb.prepared != 1 {
		t.Fatalf("restart exact=%v prepared=%d, want one exact open of rel-watched", pb.exactCalls, pb.prepared)
	}
}
