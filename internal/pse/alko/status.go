package alko

import (
	"context"
	"encoding/json"
	"strconv"
)

// Status est l'état de la source pour la console. Ni adresse, ni clé, ni lien
// n'y figurent ; l'erreur est un code générique.
type Status struct {
	State     string `json:"state"`           // idle, syncing, error
	Phase     string `json:"phase,omitempty"` // full, incremental, publish
	Read      int    `json:"read,omitempty"`  // fichiers lus pendant la lecture complète
	Files     int    `json:"files"`
	Movies    int    `json:"movies"`
	Episodes  int    `json:"episodes"`
	Published int    `json:"published"`
	Last      int64  `json:"last,omitempty"`
	LastFull  int64  `json:"lastFull,omitempty"`
	Journal   string `json:"journal,omitempty"` // ok, waiting, inactive
	Seq       int64  `json:"seq,omitempty"`
	LangFR    bool   `json:"langFR"`
	Every     int64  `json:"every"` // secondes
	Error     string `json:"error,omitempty"`
}

// Status lit l'état courant (sans contacter SFR).
func (s *Service) Status(ctx context.Context) Status {
	st := s.st()
	out := Status{State: "idle", LangFR: s.Settings.LangFR, Every: int64(s.Settings.Every.Seconds())}
	if v, _ := st.get(ctx, "counts"); v != "" {
		var c Counts
		if json.Unmarshal([]byte(v), &c) == nil {
			out.Files, out.Movies, out.Episodes, out.Published = c.Files, c.Movies, c.Episodes, c.Published
		}
	}
	out.Last, _ = st.int(ctx, "last")
	out.LastFull, _ = st.int(ctx, "last_full")
	out.Journal, _ = st.get(ctx, "journal")
	if v, _ := st.get(ctx, "seq"); v != "" {
		out.Seq, _ = strconv.ParseInt(v, 10, 64)
	}
	out.Error, _ = st.get(ctx, "error")
	s.mu.Lock()
	p := s.prog
	s.mu.Unlock()
	switch {
	case p.running:
		out.State, out.Phase, out.Read = "syncing", p.phase, p.read
	case out.Error != "":
		out.State = "error"
	}
	return out
}

// TestResult est la réponse du bouton « Tester la connexion ».
type TestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// Test lit l'état de l'API avec les réglages donnés (un seul appel, sans rien écrire).
func Test(ctx context.Context, s Settings, c *Client) TestResult {
	if p := s.Problem(); p != "" {
		return TestResult{Message: "Réglages incomplets : " + p}
	}
	if c == nil {
		c = &Client{Settings: s}
	}
	st, err := c.State(ctx)
	if err != nil {
		code := ErrorCode(err)
		return TestResult{Code: code, Message: err.Error()}
	}
	msg := "API Alkodiques joignable"
	if !st.JournalValid() {
		msg += " (journal des modifications indisponible : relecture complète périodique)"
	}
	return TestResult{OK: true, Message: msg}
}
