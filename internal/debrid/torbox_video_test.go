package debrid

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestTorBoxPlayableFileSelection(t *testing.T) {
	// Scene metadata comes first; a larger sample and a misleading video
	// extension must not displace the feature. The original list order also
	// defines explicit plugin file indices, so filtering must not renumber it.
	files := `[
		{"id":0,"name":"info.txt","size":365,"mimetype":"text/plain"},
		{"id":1,"name":"Sample/movie.sample.mkv","size":9000000000,"mimetype":"video/x-matroska"},
		{"id":2,"name":"Movie.2024.mkv","size":4000000000,"mimetype":"video/x-matroska"},
		{"id":3,"name":"artwork.mkv","size":8000000000,"mimetype":"image/jpeg"}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/torrents/mylist" || r.URL.Query().Get("id") != "77" {
			t.Errorf("unexpected request: %s", r.URL)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, `{"success":true,"data":{"id":77,"download_present":true,"files":%s}}`, files)
	}))
	defer srv.Close()
	p := newTorBox(testKey, fastOpts(srv.URL))
	index := func(i int) *int { return &i }
	for _, tc := range []struct {
		name   string
		target *core.ReleaseTarget
		reject bool
	}{
		{name: "main video"},
		{name: "original index", target: &core.ReleaseTarget{FileIndex: index(2)}},
		{name: "text index", target: &core.ReleaseTarget{FileIndex: index(0)}, reject: true},
		{name: "text path", target: &core.ReleaseTarget{ExactFile: "info.txt"}, reject: true},
		{name: "sample", target: &core.ReleaseTarget{FileIndex: index(1)}, reject: true},
		{name: "non-video MIME", target: &core.ReleaseTarget{FileIndex: index(3)}, reject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := acquireReq(true)
			req.TorrentID = "77"
			req.Choose = func(fs []File) (File, error) { return ChooseFile(fs, tc.target) }
			got, err := p.Acquire(context.Background(), req)
			if tc.reject {
				if !errors.Is(err, core.ErrReleaseRejected) {
					t.Fatalf("expected release rejection, got %+v, %v", got, err)
				}
			} else if err != nil || got.File.ID != "2" || got.File.Path != "Movie.2024.mkv" {
				t.Fatalf("expected main video, got %+v, %v", got, err)
			}
		})
	}
	files = `[{"id":0,"name":"info.txt","size":365,"mimetype":"text/plain"}]`
	req := acquireReq(true)
	req.TorrentID = "77"
	if _, err := p.Acquire(context.Background(), req); !errors.Is(err, core.ErrReleaseRejected) {
		t.Fatalf("torrent without video: %v", err)
	}
}
