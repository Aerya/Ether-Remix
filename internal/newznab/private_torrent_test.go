package newznab

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func samplePrivateTorrent(priv int) ([]byte, string) {
	info := "d" +
		bstr("length") + "i1234e" +
		bstr("name") + bstr("Private.Film.2024.1080p") +
		bstr("piece length") + "i262144e" +
		bstr("pieces") + bstr(strings.Repeat("y", 20)) +
		bstr("private") + "i" + string(rune('0'+priv)) + "e" +
		"e"
	sum := sha1.Sum([]byte(info))
	doc := "d" +
		bstr("announce") + bstr("https://tracker.private.example/announce?passkey=SECRET_PASSKEY_123") +
		bstr("info") + info +
		"e"
	return []byte(doc), hex.EncodeToString(sum[:])
}

func TestParsePrivateTorrentAndResolveWithoutTrackers(t *testing.T) {
	docPriv, wantHash := samplePrivateTorrent(1)
	meta, err := ParseTorrent(docPriv)
	if err != nil {
		t.Fatal(err)
	}
	if !meta.Private || meta.InfoHash != wantHash || meta.Name != "Private.Film.2024.1080p" {
		t.Fatalf("private meta = %+v, want Private=true hash=%s", meta, wantHash)
	}

	docPub, _ := samplePrivateTorrent(0)
	pubMeta, err := ParseTorrent(docPub)
	if err != nil {
		t.Fatal(err)
	}
	if pubMeta.Private {
		t.Fatalf("private=0 parsed as private: %+v", pubMeta)
	}

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = w.Write(docPriv)
	}))
	defer srv.Close()

	cfg := config.Indexer{ID: "priv-tz", Name: "priv-tz", URL: srv.URL, Enabled: true, Protocol: "torrent", DailyGrabs: 10}
	m := NewManager(nil, []config.Indexer{cfg}, Options{})
	ctx := context.Background()

	rel := core.Release{
		ID:       "r1",
		GUID:     "g1",
		Name:     "Private.Film.2024.1080p",
		Indexer:  "priv-tz",
		NZBURL:   srv.URL + "/dl/1.torrent",
		Protocol: core.ProtocolTorrent,
	}
	got, err := m.ResolveTorrent(ctx, rel)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Private || got.InfoHash != wantHash {
		t.Fatalf("resolved = %+v", got)
	}
	if got.MagnetURL != "magnet:?xt=urn:btih:"+wantHash {
		t.Fatalf("magnet leaked trackers or parameters: %q", got.MagnetURL)
	}
	if strings.Contains(got.MagnetURL, "SECRET_PASSKEY_123") || strings.Contains(got.MagnetURL, "tr=") {
		t.Fatalf("passkey leaked in magnet: %q", got.MagnetURL)
	}

	// Descriptor cache preserves Private=true and bare magnet without re-downloading.
	cached, err := m.ResolveTorrent(ctx, core.Release{
		ID:       "r1",
		GUID:     "g1",
		Name:     "Private.Film.2024.1080p",
		Indexer:  "priv-tz",
		NZBURL:   srv.URL + "/dl/1.torrent",
		Protocol: core.ProtocolTorrent,
	})
	if err != nil || hits != 1 || !cached.Private || cached.MagnetURL != "magnet:?xt=urn:btih:"+wantHash {
		t.Fatalf("cached resolve hits=%d cached=%+v err=%v", hits, cached, err)
	}
}
