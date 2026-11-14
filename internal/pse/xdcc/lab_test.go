package xdcc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
)

// Test d'intégration contre un vrai XDCC Grabscher branché sur le labo de
// faux bots IRC du dépôt Grabscher (tools/lab, aucun réseau public). Ignoré
// sans XDCC_LAB_URL. Voir docs/pse/XDCC.fr.md, « Tester avec le labo ».
//
//	XDCC_LAB_URL    adresse de Grabscher (ex. http://127.0.0.1:15556)
//	XDCC_LAB_KEY    clé d'API du labo
//	XDCC_LAB_MAP    correspondance des dossiers (ex. /state=/chemin/hote/state)
//	XDCC_LAB_MEDIA  dossier hôte des vidéos servies par LAB-STREAM
//	                (real.mkv, tailindex.mp4), pour vérifier les octets
func labService(t *testing.T, mode string) *Service {
	t.Helper()
	base := os.Getenv("XDCC_LAB_URL")
	if base == "" {
		t.Skip("labo Grabscher absent (XDCC_LAB_URL)")
	}
	pm, err := ParsePathMap(os.Getenv("XDCC_LAB_MAP"))
	if err != nil {
		t.Fatal(err)
	}
	s := Settings{Enabled: true, URL: base, Key: os.Getenv("XDCC_LAB_KEY"), Mode: mode, PathMap: pm, MaxActive: 4, MaxPerDay: 50,
		CacheBytes: 10 << 30, Retention: 48 * time.Hour, Buffer: 8 << 20, Wait: 10 * time.Second, MaxResults: 50, Daily: 1000}
	svc := &Service{Settings: s, Client: &Client{Base: s.URL, Key: s.Key}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	svc.Start()
	t.Cleanup(svc.Close)
	return svc
}

func labRelease(t *testing.T, s *Service, req core.SearchRequest, group string) core.Release {
	t.Helper()
	rels, err := s.Search(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rels {
		if strings.Contains(r.Name, "-"+group+".") {
			return r
		}
	}
	t.Fatalf("release %s introuvable parmi %d", group, len(rels))
	return core.Release{}
}

// playUntil relance la lecture comme un spectateur (toutes les 2 s) jusqu'à
// ce que la préparation réussisse ou échoue pour de bon.
func playUntil(t *testing.T, s *Service, rel core.Release, limit time.Duration) (core.Release, []string, error) {
	t.Helper()
	deadline := time.Now().Add(limit)
	var notes []string
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 22*time.Second)
		out, _, err := s.Prepare(ctx, rel)
		cancel()
		if err == nil {
			return out, notes, nil
		}
		msg := core.LocalDownloadMessage(err, "fr")
		if len(notes) == 0 || notes[len(notes)-1] != msg {
			notes = append(notes, msg)
		}
		if !errors.Is(err, core.ErrDownloadQueued) || time.Now().After(deadline) {
			return out, notes, err
		}
		time.Sleep(2 * time.Second)
	}
}

func ffprobe(t *testing.T, url string) (string, error) {
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe absent")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-v", "error", "-show_entries", "format=duration:stream=codec_name", "-of", "csv=p=0", url).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func ffmpegDecode(t *testing.T, url string, args ...string) (time.Duration, string, error) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg absent")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	start := time.Now()
	full := append([]string{"-v", "error", "-nostdin"}, args...)
	full = append(full, "-i", url, "-t", "20", "-f", "null", "-")
	out, err := exec.CommandContext(ctx, bin, full...).CombinedOutput()
	return time.Since(start), strings.TrimSpace(string(out)), err
}

func TestLabProgressiveMKVThroughEther(t *testing.T) {
	s := labService(t, ModeStream)
	rel := labRelease(t, s, core.SearchRequest{Type: core.TypeMovie, Title: "Lab Movie", Year: 2021}, "REMIX")
	t0 := time.Now()
	out, notes, err := playUntil(t, s, rel, 90*time.Second)
	if err != nil {
		t.Fatalf("préparation : %v (messages %q)", err, notes)
	}
	ready := time.Since(t0)
	packet, _ := PacketOf(rel)
	s.mu.Lock()
	current := s.lives[packet]
	var got int64
	if current != nil {
		got = current.current
	}
	size := s.tasks[packet].Size
	s.mu.Unlock()
	t.Logf("lisible après %s, %d/%d octets reçus ; messages au spectateur : %q", ready.Round(100*time.Millisecond), got, size, notes)
	if got >= size {
		t.Log("transfert déjà fini : la lecture progressive n'est pas démontrée par ce passage")
	}

	// Un vrai lecteur (ffmpeg, celui de Jellyfin/Emby ; Plex utilise le
	// même démuxeur) ouvre le lien pendant le transfert.
	info, err := ffprobe(t, out.StreamURL)
	t.Logf("ffprobe pendant le transfert : %q, erreur %v", info, err)
	if err != nil || !strings.Contains(info, "h264") || !strings.Contains(info, "aac") {
		t.Fatalf("ffprobe : %v %q", err, info)
	}
	took, logs, err := ffmpegDecode(t, out.StreamURL)
	t.Logf("décodage des 20 premières secondes : %s, erreur %v %s", took.Round(100*time.Millisecond), err, logs)
	if err != nil {
		t.Fatalf("décodage progressif : %v", err)
	}
	// Saut loin devant la partie reçue : résultat consigné tel quel.
	s.mu.Lock()
	got = s.lives[packet].current
	s.mu.Unlock()
	if got < size*3/4 {
		took, logs, err := ffmpegDecode(t, out.StreamURL, "-ss", "100")
		t.Logf("saut à 100 s (reçu %d %%) : %s, erreur %v %q", got*100/size, took.Round(100*time.Millisecond), err, clip(logs, 300))
	}

	// Lecture complète par le moteur d'Ether une fois le fichier reçu :
	// octets identiques à la vidéo servie par le bot.
	deadline := time.Now().Add(120 * time.Second)
	for {
		_ = s.refresh(context.Background(), true)
		s.mu.Lock()
		st := s.tasks[packet].State
		s.mu.Unlock()
		if st == StateCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("transfert jamais terminé")
		}
		time.Sleep(time.Second)
	}
	debrid.SetPSEDirectPrepare(s.Prepare)
	defer debrid.SetPSEDirectPrepare(nil)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := debrid.NewService(config.Debrid{CachedOnly: true}, debrid.Options{Logger: quiet})
	rel.ID, rel.StreamOnDemand = "lab-mkv", true
	raw, err := svc.DirectPayload(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	eng := debrid.NewEngine(svc, debrid.EngineOptions{Inner: noInner{}, Logger: quiet, HedgeDelay: -1})
	defer eng.Close()
	src, err := eng.Open(context.Background(), rel.ID, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(src, 0, src.Size())); err != nil {
		t.Fatal(err)
	}
	if media := os.Getenv("XDCC_LAB_MEDIA"); media != "" {
		want, err := os.ReadFile(filepath.Join(media, "real.mkv"))
		if err != nil {
			t.Fatal(err)
		}
		if sum := sha256.Sum256(want); !bytes.Equal(h.Sum(nil), sum[:]) {
			t.Fatal("octets lus par Ether différents de la vidéo du bot")
		}
	}
}

func TestLabTailIndexMP4WaitsForCompleteFile(t *testing.T) {
	s := labService(t, ModeStream)
	rel := labRelease(t, s, core.SearchRequest{Type: core.TypeMovie, Title: "Lab Movie", Year: 2021}, "TAIL")
	out, notes, err := playUntil(t, s, rel, 120*time.Second)
	t.Logf("messages au spectateur : %q", notes)
	if err != nil {
		t.Fatalf("MP4 jamais lisible : %v", err)
	}
	joined := strings.Join(notes, " | ")
	if !strings.Contains(joined, "ne se lit qu'une fois complet") {
		t.Fatalf("le spectateur n'a pas été prévenu que ce MP4 attend la fin : %q", joined)
	}
	packet, _ := PacketOf(rel)
	s.mu.Lock()
	state := s.tasks[packet].State
	s.mu.Unlock()
	if state != StateCompleted {
		t.Fatalf("MP4 servi avant d'être complet (%s)", state)
	}
	if info, err := ffprobe(t, out.StreamURL); err != nil || !strings.Contains(info, "h264") {
		t.Fatalf("ffprobe : %v %q", err, info)
	}
}

func TestLabSilentAndRefusingBotsAreExplained(t *testing.T) {
	s := labService(t, ModeStream)
	cases := []struct {
		req   core.SearchRequest
		group string
	}{
		{core.SearchRequest{Type: core.TypeMovie, Title: "Lab Movie", Year: 2021}, "MUTE"},
		{core.SearchRequest{Type: core.TypeEpisode, Title: "Lab Show", Season: 1, Episode: 2}, "NOPE"},
	}
	for _, c := range cases {
		rel := labRelease(t, s, c.req, c.group)
		_, notes, err := playUntil(t, s, rel, 150*time.Second)
		t.Logf("%s : %v ; messages %q", c.group, err, notes)
		if err == nil || errors.Is(err, core.ErrDownloadQueued) {
			t.Fatalf("%s : échec attendu, %v", c.group, err)
		}
		if msg := core.LocalDownloadMessage(err, "fr"); !strings.Contains(msg, "XDCC") {
			t.Fatalf("%s : message %q", c.group, msg)
		}
		// Ensuite la recherche ne propose plus ce paquet.
		rels, _ := s.Search(context.Background(), c.req, nil)
		for _, r := range rels {
			if r.GUID == rel.GUID {
				t.Fatalf("%s encore proposé après son échec", c.group)
			}
		}
	}
}

func TestLabCacheCleanupKeepsKeptFiles(t *testing.T) {
	s := labService(t, ModeStream)
	ep := labRelease(t, s, core.SearchRequest{Type: core.TypeEpisode, Title: "Lab Show", Season: 1, Episode: 1}, "REMIX")
	packet, _ := PacketOf(ep)
	if _, err := s.Grab(context.Background(), packet, true); err != nil { // manuel, gardé
		t.Fatal(err)
	}
	deadline := time.Now().Add(120 * time.Second)
	for {
		_ = s.refresh(context.Background(), true)
		s.mu.Lock()
		tk := *s.tasks[packet]
		s.mu.Unlock()
		if tk.State == StateCompleted {
			local, _ := s.Settings.Local(tk.Storage)
			if !strings.Contains(tk.Storage, "/"+CategoryKeep+"/") || localSize(local) != tk.Size {
				t.Fatalf("fichier gardé mal rangé : %s", tk.Storage)
			}
			// Conservation dépassée pour tout le cache : rien de gardé n'est effacé.
			s.Settings.Retention = time.Nanosecond
			s.mu.Lock()
			for _, x := range s.tasks {
				x.LastRead, x.Done = time.Time{}, time.Now().Add(-time.Hour)
			}
			s.mu.Unlock()
			s.clean(context.Background(), true)
			if localSize(local) != tk.Size {
				t.Fatal("fichier gardé effacé par le nettoyage du cache")
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			for p, x := range s.tasks {
				if p != packet && !x.Keep && x.State == StateCompleted {
					t.Fatalf("fichier du cache expiré non effacé : %s", x.Name)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("téléchargement manuel jamais terminé (%s)", tk.State)
		}
		time.Sleep(time.Second)
	}
}
