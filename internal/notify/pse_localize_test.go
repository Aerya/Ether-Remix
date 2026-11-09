package notify

// Test propre à Private Streaming Enhanced : la traduction s'applique à
// l'envoi réel vers un canal, et se désactive avec PSE_NOTIFY_LANGUAGE=en.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/pse/notifyfr"
)

func TestPSEDeliveredNotificationsAreLocalized(t *testing.T) {
	for _, tc := range []struct {
		lang, want string
	}{
		{"fr", "Titre disponible\nDune"},
		{"en", "Title available\nDune"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			t.Setenv(notifyfr.EnvLanguage, tc.lang)
			got := make(chan string, 1)
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Content string `json:"content"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				got <- body.Content
				w.WriteHeader(204)
			}))
			defer up.Close()
			ctx := context.Background()
			s := newTestService(t)
			if err := s.Save(ctx, Channel{ID: "discord", Kind: "discord", Secrets: map[string]string{"endpoint": up.URL}, Enabled: true, Events: []string{"*"}}); err != nil {
				t.Fatal(err)
			}
			if err := s.TitleAvailable(ctx, "Dune"); err != nil {
				t.Fatal(err)
			}
			if err := s.Drain(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case content := <-got:
				if content != tc.want {
					t.Fatalf("contenu Discord = %q, attendu %q", content, tc.want)
				}
			default:
				t.Fatal("aucune notification reçue")
			}
		})
	}
}
