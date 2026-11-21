package music

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// Test réel facultatif : OPENDEEZER_LIVE_ARL=<arl> go test ./internal/pse/music -run OpenDeezerLive -v
// L'ARL vient seulement de l'environnement ; il n'est jamais affiché. Ne lit
// que quelques secondes du flux, sans rien enregistrer.
func TestOpenDeezerLive(t *testing.T) {
	arl := os.Getenv("OPENDEEZER_LIVE_ARL")
	if arl == "" {
		t.Skip("OPENDEEZER_LIVE_ARL absent")
	}
	allowLocalAudio = false
	defer func() { allowLocalAudio = true }()
	o := NewOpenDeezer(arl, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	offer, err := o.Status(ctx)
	if err != nil {
		t.Fatal(strings.ReplaceAll(err.Error(), arl, "[ARL]"))
	}
	t.Logf("compte : %s", offer)
	// Titre long et courant : « Daft Punk — Around the World » (3135556 est
	// « Harder, Better… »), durée > 1 min pour détecter un aperçu.
	st, err := o.Resolve(ctx, Track{ID: 3135556, Duration: 224})
	if err != nil {
		t.Skipf("titre non lisible en entier avec ce compte : %v", strings.ReplaceAll(err.Error(), arl, "[ARL]"))
	}
	rc, _, _ := st.Open()
	defer rc.Close()
	size, _ := rc.Seek(0, io.SeekEnd)
	t.Logf("qualité %s, taille complète annoncée : %d octets", st.Quality, size)
	if size < 1<<20 {
		t.Fatalf("flux trop court pour un titre complet : %d", size)
	}
	// Octets déchiffrés : un MP3 valide commence par une balise ID3 ou une synchro de trame.
	_, _ = rc.Seek(0, io.SeekStart)
	head := make([]byte, 3)
	if _, err := io.ReadFull(rc, head); err != nil || !(string(head) == "ID3" || (head[0] == 0xFF && head[1]&0xE0 == 0xE0)) {
		t.Fatalf("début de flux non déchiffré correctement : % x %v", head, err)
	}
	// Déplacement au milieu, lecture de 256 Kio puis arrêt : pas de téléchargement complet.
	_, _ = rc.Seek(size/2, io.SeekStart)
	n, err := io.CopyN(io.Discard, rc, 256<<10)
	if err != nil || n != 256<<10 {
		t.Fatalf("lecture après déplacement : %d %v", n, err)
	}
}
