package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestIndexerThrottledErrorChain(t *testing.T) {
	err := fmt.Errorf("search: %w", &IndexerThrottledError{Indexers: []string{"Nzb A"}, RetryAt: time.Now().Add(15 * time.Minute), Err: ErrBudgetExhausted})
	var thr *IndexerThrottledError
	if !errors.Is(err, ErrIndexerThrottled) || !errors.Is(err, ErrBudgetExhausted) || !errors.As(err, &thr) {
		t.Fatalf("chain broken: %v", err)
	}
}

func TestIndexerThrottleMessages(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.Local)
	hhmm := func(d time.Duration) string { return now.Add(d).Format("15:04") }
	for _, tc := range []struct {
		name string
		e    IndexerThrottledError
		want string
	}{
		{"un indexeur", IndexerThrottledError{Indexers: []string{"Nzb A"}, RetryAt: now.Add(15 * time.Minute)},
			"Nzb A limite les requêtes d'Ether : reprise dans environ 15 min (" + hhmm(15*time.Minute) + ")."},
		{"autres sans release", IndexerThrottledError{Indexers: []string{"Nzb A"}, RetryAt: now.Add(15 * time.Minute), OthersAnswered: true},
			"Nzb A limite les requêtes d'Ether : reprise dans environ 15 min (" + hhmm(15*time.Minute) + "). Aucune autre source n'a de release pour ce titre."},
		{"plusieurs", IndexerThrottledError{Indexers: []string{"A", "B", "C"}, RetryAt: now.Add(90*time.Minute + 20*time.Second)},
			"A, B et C limitent les requêtes d'Ether : reprise dans environ 1 h 30 (" + hhmm(90*time.Minute+20*time.Second) + ")."},
		{"heure pleine", IndexerThrottledError{Indexers: []string{"A"}, RetryAt: now.Add(2 * time.Hour)},
			"A limite les requêtes d'Ether : reprise dans environ 2 h (" + hhmm(2*time.Hour) + ")."},
		{"reprise inconnue", IndexerThrottledError{Indexers: []string{"A"}},
			"A limite les requêtes d'Ether : nouvelle tentative automatique plus tard."},
	} {
		if got := IndexerThrottleMessage(&tc.e, now); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
	if got := IndexerThrottleMessage(&IndexerThrottledError{Indexers: []string{"A"}, RetryAt: now.Add(10 * time.Second)}, now); !strings.Contains(got, "environ 1 min") {
		t.Errorf("minimum 1 min: %q", got)
	}
}

func TestLocalDownloadMessageIndexers(t *testing.T) {
	thr := fmt.Errorf("x: %w", &IndexerThrottledError{Indexers: []string{"Nzb A"}, RetryAt: time.Now().Add(15 * time.Minute), Err: ErrBudgetExhausted})
	if got := LocalDownloadMessage(thr, ""); !strings.HasPrefix(got, "Nzb A limite les requêtes d'Ether : reprise dans environ ") {
		t.Errorf("pause: %q", got)
	}
	if got := thr.Error(); !strings.Contains(got, "limite les requêtes d'Ether") {
		t.Errorf("history text: %q", got)
	}
	lim := &IndexerDailyLimitError{Indexers: []string{"Nzb A"}, Err: ErrBudgetExhausted}
	want := "Limite journalière de requêtes réglée dans Ether atteinte pour Nzb A."
	if got := LocalDownloadMessage(lim, ""); got != want {
		t.Errorf("daily: %q", got)
	}
	if errors.Is(lim, ErrIndexerThrottled) || !errors.Is(lim, ErrBudgetExhausted) {
		t.Error("daily limit must be a budget error, not a pause")
	}
	if got := LocalDownloadMessage(ErrBudgetExhausted, ""); got != "" {
		t.Errorf("plain budget error: %q", got)
	}
}

func TestErrorKeepsSafeDetail(t *testing.T) {
	e := &IndexerThrottledError{Indexers: []string{"A"}, Err: fmt.Errorf("get https://x.test/api?apikey=SECRET: %w", ErrBudgetExhausted)}
	got := e.Error()
	if !strings.Contains(got, "(") || !strings.Contains(got, ErrBudgetExhausted.Error()) || strings.Contains(got, "SECRET") || strings.Contains(got, "x.test") {
		t.Fatalf("detail: %q", got)
	}
}

func TestErrDetailMasksSchemesSecretsAndCutsRunes(t *testing.T) {
	d := errDetail(errors.New("HTTPS://host/x?apikey=s3cret sftp://u:p@h/ host/api?apikey=hidden&t=1 token=abc"))
	for _, leak := range []string{"s3cret", "u:p@", "hidden", "abc"} {
		if strings.Contains(d, leak) {
			t.Fatalf("detail leaks %q: %s", leak, d)
		}
	}
	long := errDetail(errors.New(strings.Repeat("é", 300)))
	if !utf8.ValidString(long) || utf8.RuneCountInString(long) != 200+4 {
		t.Fatalf("bad cut: %d runes, valid=%v", utf8.RuneCountInString(long), utf8.ValidString(long))
	}
}
