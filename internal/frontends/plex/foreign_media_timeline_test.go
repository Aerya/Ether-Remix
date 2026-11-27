package plex

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

// A trailer, theme or preview played on a title's detail page never counts as
// watching the title: not by the title's key with the trailer's duration, and
// not by the trailer's key beside the title's play-queue item. Either used to
// finish the film and seed "Because you watched" rows.
func TestTrailerTimelineNeverCountsForItsTitle(t *testing.T) {
	f, _, _ := extrasFixture(t)
	watch := state.New(store.OpenTest(t), state.Options{})
	f.srv.setState(watch)
	trailer := fightClubTrailer.Duration.Milliseconds()

	// The trailer plays from the start to its end, as a real player reports it.
	type beat struct {
		state string
		at    int64
	}
	plays := []beat{{"playing", 3000}, {"playing", trailer / 2}, {"playing", trailer - 5000}, {"stopped", trailer}}

	// The client names the film but plays the two-minute trailer.
	for _, b := range plays {
		rec := f.get(t, fmt.Sprintf("/:/timeline?ratingKey=%s&key=/library/metadata/%s&state=%s&time=%d&duration=%d",
			fightClub.Key, fightClub.Key, b.state, b.at, trailer), "X-Plex-Client-Identifier", "detail-page")
		if rec.Code != http.StatusOK {
			t.Fatalf("%+v: %d %s", b, rec.Code, rec.Body.String())
		}
	}

	// The client names the trailer and the film's queue item beside it.
	uri := "server://0123456789abcdef0123456789abcdef01234567/com.plexapp.plugins.library/library/metadata/" + fightClub.Key.String()
	mc := decodeJSON(t, f.request(t, http.MethodPost, "/playQueues?type=video&extrasPrefixCount=1&uri="+urlEsc(uri),
		map[string]string{"Accept": "application/json", "X-Plex-Client-Identifier": "detail-page"}))
	items := mc["Metadata"].([]any)
	feature := items[len(items)-1].(map[string]any)
	if feature["ratingKey"] != fightClub.Key.String() {
		t.Fatalf("queue: %v", items)
	}
	for _, b := range plays {
		rec := f.get(t, fmt.Sprintf("/:/timeline?ratingKey=%s&key=/library/metadata/%s&playQueueItemID=%v&state=%s&time=%d&duration=%d",
			fightClubTrailer.Key, fightClubTrailer.Key, feature["playQueueItemID"], b.state, b.at, trailer), "X-Plex-Client-Identifier", "detail-page")
		if rec.Code != http.StatusOK {
			t.Fatalf("queued %+v: %d %s", b, rec.Code, rec.Body.String())
		}
	}

	ctx := context.Background()
	got, err := watch.Get(ctx, ownerUser.ID, fightClub.Key)
	if err != nil {
		t.Fatal(err)
	}
	if got.ViewCount != 0 || got.ViewOffset != 0 || got.Duration != 0 || !got.LastViewedAt.IsZero() {
		t.Fatalf("a trailer wrote the film's watch state: %+v", got)
	}
	history, err := watch.History(ctx, ownerUser.ID, time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("a trailer logged the film's history: %+v", history)
	}
}
