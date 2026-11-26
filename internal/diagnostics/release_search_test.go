package diagnostics

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Release search exports release names and query text, but never the
// owner's indexer names, hosts or keys, and drops titles without the flag.
func TestReleaseSearchHidesIndexers(t *testing.T) {
	at := time.Date(2026, 11, 24, 22, 14, 0, 0, time.UTC)
	in := ReleaseSearchInput{
		IndexerNames: []string{"SecretTracker", "PrivNZB"},
		Attempts: []ResolverAttempt{{
			At: at, Title: "Home Alone S01E674", Error: `indexer "SecretTracker": timeout via https://tracker.example.com/api?apikey=k3y`,
			Queries: []ResolverQuery{
				{Indexer: "SecretTracker", Kind: "torznab", Mode: "tvsearch", Query: "I Live Alone", Season: "1", Episode: "674", Results: 0, Error: "SecretTracker answered 503"},
				{Indexer: "PrivNZB", Kind: "newznab", Mode: "search", Query: "Na Honja Sanda E674", Results: 2},
			},
			Candidates: []ResolverCandidate{
				{Name: "Na.Honja.Sanda.E674.1080p.WEB-DL", Indexer: "PrivNZB", Rejected: true, Reason: "names the other title"},
				{Name: "Na.Honja.Sanda.E673.1080p.WEB-DL-PRIVNZB", Indexer: "PrivNZB", Rejected: true, Reason: "names the other title"},
				{Name: "Monster.By.Mistake.S01E09.Home.Alone.WEB", Indexer: "SecretTracker", Rejected: true, Reason: "title mismatch"},
			},
		}},
	}
	uses, _ := prepareSecrets([]string{"k3y"})
	for _, titles := range []bool{true, false} {
		doc := releaseSearchDocument(in, titles, uses)
		body, _ := json.Marshal(doc)
		var report bytes.Buffer
		writeReleaseSearch(&report, []archived{{name: "release-search.json", body: body}})
		for _, text := range []string{string(body), report.String()} {
			for _, banned := range []string{"SecretTracker", "PrivNZB", "tracker.example.com", "k3y"} {
				if strings.Contains(text, banned) {
					t.Fatalf("titles=%v: %q leaked:\n%s", titles, banned, text)
				}
			}
			for _, want := range []string{"indexer A", "indexer B", "names the other title"} {
				if !strings.Contains(text, want) {
					t.Fatalf("titles=%v: missing %q:\n%s", titles, want, text)
				}
			}
			if strings.Contains(text, "I Live Alone") != titles || strings.Contains(text, "Na.Honja.Sanda.E674") != titles {
				t.Fatalf("titles=%v: title text handling wrong:\n%s", titles, text)
			}
		}
		a := doc.Attempts[0]
		if titles && !strings.Contains(string(body), "Monster.By.Mistake.S01E09.Home.Alone.WEB") {
			t.Fatalf("dotted release name was mangled:\n%s", body)
		}
		if len(a.Rejections) != 2 || a.Rejections[0].Count != 2 || a.Outcome != "no release accepted" {
			t.Fatalf("attempt %+v", a)
		}
	}
}
