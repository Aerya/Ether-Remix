package music

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	allowLocalAudio = true // les tests servent l'audio depuis 127.0.0.1
	os.Exit(m.Run())
}

func TestCheckAudioHost(t *testing.T) {
	allowLocalAudio = false
	defer func() { allowLocalAudio = true }()
	ctx := context.Background()
	for _, bad := range []string{"http://127.0.0.1/a.flac", "http://localhost:8080/a", "http://169.254.169.254/latest", "http://[::1]/x", "file:///etc/passwd", "http://0.0.0.0/"} {
		if checkAudioHost(ctx, bad) == nil {
			t.Fatalf("%s accepté", bad)
		}
	}
	for _, ok := range []string{"https://203.0.113.5/a.flac", "http://192.168.1.10/a.flac", "http://10.0.0.2/s"} {
		if err := checkAudioHost(ctx, ok); err != nil {
			t.Fatalf("%s refusé : %v", ok, err)
		}
	}
}

func TestVersionOf(t *testing.T) {
	if !sameVersion("Song (Remastered 2011)", "Song") || !sameVersion("Song - Radio Edit", "Song") {
		t.Fatal("mention neutre comptée comme version")
	}
	if sameVersion("Song (Live at Wembley)", "Song") || sameVersion("Song (Club Remix)", "Song") || sameVersion("Song", "Song (A Cappella)") {
		t.Fatal("version différente confondue")
	}
	if !sameVersion("Live Forever", "Live Forever (Remastered)") {
		t.Fatal("mot du titre pris pour une version")
	}
}

type fixedResolver struct{ url string }

func (f fixedResolver) Resolve(context.Context, Track) (Stream, error) {
	return Stream{URL: f.url, Mime: "audio/flac", Quality: "LOSSLESS"}, nil
}

// failoverSource sert les flux de Resolvers, comme DeezerSource.
type failoverSource struct {
	Source
	rs Resolvers
}

func (f failoverSource) Stream(ctx context.Context, id int64, _ string) (Stream, error) {
	return f.rs.Resolve(ctx, Track{ID: id})
}

// Un lien refusé (expiré, 403) fait passer à la source suivante.
func TestRelayFailsOverToNextSource(t *testing.T) {
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		if r.URL.Path == "/expired.flac" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "audio/flac")
		_, _ = w.Write([]byte("fLaC"))
	}))
	defer srv.Close()
	s := NewServer(Options{Source: failoverSource{rs: Resolvers{fixedResolver{srv.URL + "/expired.flac"}, fixedResolver{srv.URL + "/ok.flac"}}}})
	rec := httptest.NewRecorder()
	if err := s.relayAudio(rec, httptest.NewRequest(http.MethodGet, "/rest/stream", nil), 1); err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != "fLaC" || hits["/expired.flac"] != 1 || hits["/ok.flac"] != 1 {
		t.Fatalf("corps %q, appels %v", rec.Body.String(), hits)
	}
	// Toutes les sources refusées : erreur, rien n'est écrit.
	s = NewServer(Options{Source: failoverSource{rs: Resolvers{fixedResolver{srv.URL + "/expired.flac"}}}})
	rec = httptest.NewRecorder()
	if err := s.relayAudio(rec, httptest.NewRequest(http.MethodGet, "/rest/stream", nil), 1); err == nil || rec.Body.Len() != 0 {
		t.Fatalf("err %v, corps %q", err, rec.Body.String())
	}
}
