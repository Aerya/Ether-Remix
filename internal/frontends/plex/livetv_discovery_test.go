package plex

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

type discoveryLineup struct {
	frontend.LiveTV
	scans, guideCalls atomic.Int64
}

func (*discoveryLineup) Enabled() bool                                  { return true }
func (*discoveryLineup) VisibleTo(context.Context, *frontend.User) bool { return true }
func (*discoveryLineup) LineupChangedFor(*frontend.User) time.Time      { return fixedNow }
func (d *discoveryLineup) AllowedChannels(context.Context, *frontend.User) ([]frontend.LiveTVChannel, error) {
	d.scans.Add(1)
	cs := make([]frontend.LiveTVChannel, 10000)
	for i := range cs {
		cs[i] = frontend.LiveTVChannel{ID: fmt.Sprint(i), Number: fmt.Sprint(i + 1), Name: fmt.Sprintf("Channel %d", i+1), Enabled: true}
	}
	return cs, nil
}
func (d *discoveryLineup) GuideForChannels(_ context.Context, _ *frontend.User, _, _ time.Time, ids []string) ([]frontend.LiveTVProgramme, error) {
	d.guideCalls.Add(1)
	if len(ids) > 32 {
		return nil, fmt.Errorf("unbounded guide batch: %d", len(ids))
	}
	out := make([]frontend.LiveTVProgramme, len(ids))
	for i, id := range ids {
		out[i] = frontend.LiveTVProgramme{ID: id + "-1", ChannelID: id, Start: fixedNow.Add(-time.Hour), End: fixedNow.Add(time.Hour), Item: frontend.LiveTVItem{Title: "Programme", Genres: []string{"犯罪"}}}
	}
	return out, nil
}
func (d *discoveryLineup) FilterDiscovery(_ context.Context, _ *frontend.User, ps []frontend.LiveTVProgramme) ([]frontend.LiveTVProgramme, []frontend.LiveTVChannel, error) {
	if len(ps) > liveDiscoveryItemsMax {
		return nil, nil, fmt.Errorf("unbounded policy candidates: %d", len(ps))
	}
	cs := make([]frontend.LiveTVChannel, len(ps))
	for i, p := range ps {
		cs[i] = frontend.LiveTVChannel{ID: p.ChannelID, Number: p.ChannelID, Name: p.ChannelID}
	}
	return ps, cs, nil
}

// Discovery's load-bearing invariant: lineup-sized preparation stays out of
// requests; cached shelf and programme-policy work have fixed candidate caps.
func TestLiveDiscoveryBoundedProjection(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	d := &discoveryLineup{}
	f.srv.setLiveTV(d)
	f.srv.setTestSubscription(true)
	viewer := &frontend.User{ID: 1, IsOwner: true}
	f.srv.liveDiscoverySnapshot(context.Background(), viewer)
	f.srv.liveDiscovery.mu.Lock()
	ready := f.srv.liveDiscovery.ready
	f.srv.liveDiscovery.mu.Unlock()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("preparation did not finish")
	}
	snapshot := f.srv.liveDiscoverySnapshot(context.Background(), viewer)
	if snapshot == nil {
		t.Fatal("snapshot absent")
	}
	if len(snapshot.buckets) > liveDiscoveryBucketsMax {
		t.Fatal("bucket cap exceeded")
	}
	if len(snapshot.genres) != 10000 {
		t.Fatalf("genre membership lost channels: %d", len(snapshot.genres))
	}
	found := false
	for _, b := range snapshot.buckets {
		if len(b.items) > liveDiscoveryItemsMax {
			t.Fatal("item cap exceeded")
		}
		if b.id == liveGenreID("犯罪") {
			found = true
			if b.title != "犯罪" {
				t.Fatal("provider label changed")
			}
		}
	}
	if !found {
		t.Fatal("non-English guide category lost")
	}
	scans, calls := d.scans.Load(), d.guideCalls.Load()
	r := httptest.NewRequest("GET", liveProviderPath+"/watchnow/all", nil)
	r = r.WithContext(withRequest(r.Context(), &Request{User: viewer}))
	ps, _, err := f.srv.liveDiscoveryItems(r, "all", liveDiscoveryItemsMax)
	if err != nil || len(ps) != liveDiscoveryItemsMax {
		t.Fatalf("items=%d err=%v", len(ps), err)
	}
	if scans != d.scans.Load() || calls != d.guideCalls.Load() {
		t.Fatal("request rescanned lineup/guide")
	}
	numbers := []string{"10", "4.10", "2", "4.2", "NaN", "1"}
	sort.Slice(numbers, func(i, j int) bool { return compareChannelNum(numbers[i], numbers[j]) })
	if fmt.Sprint(numbers) != "[1 2 4.2 4.10 10 NaN]" {
		t.Fatalf("natural ordering: %v", numbers)
	}
}
