package cardigann

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func privateTorrentBytes(name string) ([]byte, string) {
	bstr := func(s string) string { return fmt.Sprintf("%d:%s", len(s), s) }
	info := "d" + bstr("length") + "i1024e" + bstr("name") + bstr(name) + bstr("piece length") + "i262144e" + bstr("pieces") + bstr(strings.Repeat("p", 20)) + bstr("private") + "i1ee"
	sum := sha1.Sum([]byte(info))
	doc := "d" + bstr("announce") + bstr("https://tracker.private.example/announce?passkey=CARDIGANN_SECRET") + bstr("info") + info + "e"
	return []byte(doc), hex.EncodeToString(sum[:])
}

func TestPrivateDefinitionMarksReleasesPrivateAndStripsTrackers(t *testing.T) {
	site := newPrivateSite(t)
	def := loadDef(t, "private.yml")
	r := testRunner(t, def, site.srv.URL, runnerSetup{settings: aliceSettings})
	ctx := context.Background()

	rels, err := r.Search(ctx, core.SearchRequest{Type: core.TypeMovie, Title: "Private Film", Year: 2020}, nil, nil, nil)
	if err != nil || len(rels) != 1 {
		t.Fatalf("search: %v %+v", err, rels)
	}
	if !rels[0].Private {
		t.Fatalf("private definition did not mark release Private: %+v", rels[0])
	}
	got, err := r.ResolveTorrent(ctx, rels[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Private || got.MagnetURL != "magnet:?xt=urn:btih:"+site.hash {
		t.Fatalf("resolved private release = %+v, want bare magnet", got)
	}

	// A public definition whose downloaded .torrent has info.private == 1 also becomes Private with bare magnet.
	doc, wantHash := privateTorrentBytes("Film.2024.1080p")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = w.Write(doc)
	}))
	defer srv.Close()
	pub := testRunner(t, loadDef(t, "public.yml"), srv.URL, runnerSetup{})
	resolved, err := pub.ResolveTorrent(ctx, core.Release{
		Name:     "Film.2024.1080p",
		NZBURL:   srv.URL + "/download.php?id=1",
		Protocol: core.ProtocolTorrent,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Private || resolved.InfoHash != wantHash || resolved.MagnetURL != "magnet:?xt=urn:btih:"+wantHash {
		t.Fatalf("public site with private .torrent = %+v", resolved)
	}
}
