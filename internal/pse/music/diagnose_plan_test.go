package music

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Le plan suit l'ordre réel de lecture, détaille chaque compte, explique
// les sources inactives et n'ajoute jamais HiFi API en repli.
func TestDiagnosePlanFollowsRealChain(t *testing.T) {
	cfg := Settings{
		EclipseURLs: "https://eclipse.example/SECRET-TOKEN/manifest.json",
		RealDebrid:  true, TorBox: true, Audius: "last",
		HiFiURL: "https://user:pass@hifi.example/api",
	}
	debrid := DebridSources{
		{Kind: "torbox", Name: "TorBox", Resolver: Resolvers{
			&DebridResolver{Provider: "TorBox #1"}, &DebridResolver{Provider: "TorBox #2"},
		}},
		{Kind: "realdebrid", Name: "Real-Debrid", Resolver: &DebridResolver{}, NoAccount: true},
		{Kind: "alldebrid", Name: "AllDebrid", Resolver: &DebridResolver{}},
	}
	rs, plan := chain(cfg, debrid, nil)
	var names []string
	for _, r := range rs {
		names = append(names, r.name)
	}
	want := []string{"Addon Eclipse · eclipse.example", "TorBox #1", "TorBox #2", "Audius"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("chaîne %v, %v attendue", names, want)
	}
	reasons := map[string]string{}
	for _, p := range plan {
		if p.Active {
			if p.Order == 0 || names[p.Order-1] != p.Name {
				t.Fatalf("rang faux : %+v", p)
			}
			continue
		}
		reasons[p.Kind] = p.Reason
	}
	if !strings.Contains(reasons["realdebrid"], "Aucun compte") {
		t.Fatalf("Real-Debrid sans compte : %q", reasons["realdebrid"])
	}
	if !strings.Contains(reasons["alldebrid"], "non activée") {
		t.Fatalf("AllDebrid non activé : %q", reasons["alldebrid"])
	}
	if !strings.Contains(reasons["hifi"], "aucune autre source") {
		t.Fatalf("HiFi doit rester hors chaîne : %q", reasons["hifi"])
	}
	b, _ := json.Marshal(plan)
	for _, secret := range []string{"SECRET-TOKEN", "user:pass", "/api"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("plan expose %q : %s", secret, b)
		}
	}
}

func TestDiagnosePlanHiFiAlone(t *testing.T) {
	rs, plan := chain(Settings{HiFiURL: "https://hifi.example"}, nil, nil)
	if len(rs) != 0 {
		t.Fatal("HiFi API n'est pas un résolveur de la chaîne")
	}
	for _, p := range plan {
		if p.Kind == "hifi" && (!p.Active || p.Order != 1) {
			t.Fatalf("HiFi seule doit être active : %+v", p)
		}
	}
}

// Les étapes distinguent lien joignable et octets audio vérifiés.
func TestProbeStages(t *testing.T) {
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte{0xff, 0xfb, 0x90, 0x00})
	}))
	defer audio.Close()
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/flac")
		_, _ = w.Write([]byte(`<html>refusé</html>`))
	}))
	defer html.Close()
	var ok, bad SourceReport
	if err := probe(context.Background(), audio.Client(), Stream{URL: audio.URL}, &ok); err != nil || ok.Stage != "bytes" {
		t.Fatalf("audio : %q %v", ok.Stage, err)
	}
	if err := probe(context.Background(), html.Client(), Stream{URL: html.URL}, &bad); err == nil || bad.Stage != "link" {
		t.Fatalf("faux audio : %q %v", bad.Stage, err)
	}
}
