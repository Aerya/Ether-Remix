package ftp

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Essais réels de lecture : vraies vidéos générées par ffmpeg, lues par
// ffprobe et ffmpeg à travers le moteur d'Ether (DirectPayload puis
// lecture par blocs), avec sauts au milieu et à la fin. Ignorés sans
// FTP_LAB=1 (quelques dizaines de secondes, ffmpeg requis).
//
// Sans autre variable, le serveur est le SFTP intégré au test. Avec un
// laboratoire en conteneurs locaux (voir docs/pse/FTP.fr.md, « Tester avec
// le labo »), les mêmes essais visent de vrais serveurs :
//
//	FTP_LAB_DIR        dossier hôte servi par les conteneurs (vidéos écrites ici)
//	FTP_LAB_SFTP       adresse du serveur SFTP (ex. 127.0.0.1:2222)
//	FTP_LAB_SFTP_ROOT  dossier vu par le compte SFTP (ex. /upload)
//	FTP_LAB_FTPS       adresse du serveur FTPS explicite (ex. 127.0.0.1:2121)
//	FTP_LAB_FTPS_ROOT  dossier vu par le compte FTPS (ex. /)
//	FTP_LAB_USER, FTP_LAB_PASS  compte de test des conteneurs

func labMedia(t *testing.T, dir string) map[string]string {
	t.Helper()
	if os.Getenv("FTP_LAB") == "" {
		t.Skip("essais réels désactivés (FTP_LAB=1)")
	}
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg absent")
	}
	out := map[string]string{
		"mkv": "films/Film.Reel.2020.1080p.WEB.x264-LAB/Film.Reel.2020.1080p.WEB.x264-LAB.mkv",
		// MP4 sans « faststart » : index en fin de fichier, illisible par
		// une source séquentielle (XDCC) avant la fin, lisible ici.
		"mp4": "films/Film.Fin.2021.1080p.WEB.x264-LAB.mp4",
	}
	for kind, rel := range out {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if st, err := os.Stat(p); err == nil && st.Size() > minVideoSize {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
			"-t", "70", "-c:v", "libx264", "-preset", "ultrafast", "-b:v", "6M", "-maxrate", "6M", "-bufsize", "12M", "-g", "50", "-c:a", "aac", "-shortest"}
		if kind == "mp4" {
			args = append(args, "-movflags", "-faststart")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		b, err := exec.CommandContext(ctx, bin, append(args, p)...).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("ffmpeg : %v %s", err, b)
		}
	}
	return out
}

// serveEther expose une source du moteur d'Ether en HTTP, comme Remix le
// fait pour Plex, Jellyfin ou Emby (internal/playback/serve.go : chaque
// réponse lit avec ReadAtConcurrentContext).
func serveEther(t *testing.T, src core.MediaSource, name string) string {
	t.Helper()
	cc, ok := src.(interface {
		ReadAtConcurrentContext(context.Context, []byte, int64) (int, error)
	})
	if !ok {
		t.Fatal("lecture concurrente absente du moteur")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rd := io.NewSectionReader(readerAt(func(p []byte, off int64) (int, error) { return cc.ReadAtConcurrentContext(r.Context(), p, off) }), 0, src.Size())
		http.ServeContent(w, r, name, time.Time{}, rd)
	}))
	t.Cleanup(ts.Close)
	return ts.URL + "/" + name
}

type readerAt func([]byte, int64) (int, error)

func (f readerAt) ReadAt(p []byte, off int64) (int, error) { return f(p, off) }

func ffprobe(t *testing.T, url string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration:stream=codec_name", "-of", "csv=p=0", url).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe : %v %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// ffmpegAt décode 5 s à partir de la position at (saut).
func ffmpegAt(t *testing.T, url string, at int) time.Duration {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	out, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-nostdin", "-ss", strconv.Itoa(at), "-i", url, "-t", "5", "-f", "null", "-").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("ffmpeg à %d s : %v %s", at, err, out)
	}
	return time.Since(start)
}

func labFlow(t *testing.T, s Settings, media map[string]string) {
	t.Helper()
	svc := newService(t, s)
	for kind, want := range map[string]core.SearchRequest{
		"mkv": {Type: core.TypeMovie, Title: "Film Reel", Year: 2020},
		"mp4": {Type: core.TypeMovie, Title: "Film Fin", Year: 2021},
	} {
		rel := one(t, svc, want)
		src := etherSource(t, svc, rel)
		url := serveEther(t, src, "film."+kind)
		info := ffprobe(t, url)
		if !strings.Contains(info, "h264") || !strings.Contains(info, "aac") || !strings.Contains(info, "70.") && !strings.Contains(info, "69.") {
			t.Fatalf("%s : ffprobe %q", kind, info)
		}
		var times []string
		for _, at := range []int{0, 60, 30, 5} {
			times = append(times, strconv.Itoa(at)+" s → "+ffmpegAt(t, url, at).Round(10*time.Millisecond).String())
		}
		t.Logf("%s %s (%s) : ffprobe %q ; décodage de 5 s après saut : %s", s.Protocol, kind, media[kind], strings.ReplaceAll(info, "\n", " "), strings.Join(times, ", "))
	}
}

// TestLabStreamsRealVideosOverBuiltinSFTP : serveur SFTP intégré au test.
func TestLabStreamsRealVideosOverBuiltinSFTP(t *testing.T) {
	dir := t.TempDir()
	if d := os.Getenv("FTP_LAB_CACHE"); d != "" {
		dir = d // vidéos gardées d'un essai à l'autre
	}
	media := labMedia(t, dir)
	srv := startSFTP(t, dir)
	labFlow(t, sftpSettings(srv.addr, srv.fingerprint), media)
}

func labSettings(t *testing.T, proto, addrVar, rootVar string) (Settings, map[string]string) {
	t.Helper()
	addr := os.Getenv(addrVar)
	if addr == "" || os.Getenv("FTP_LAB_DIR") == "" {
		t.Skip("laboratoire en conteneurs absent (" + addrVar + ", FTP_LAB_DIR)")
	}
	media := labMedia(t, os.Getenv("FTP_LAB_DIR"))
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(port)
	root := os.Getenv(rootVar)
	if root == "" {
		root = "/"
	}
	s := Settings{Enabled: true, Protocol: proto, Host: host, Port: p, User: os.Getenv("FTP_LAB_USER"), Password: os.Getenv("FTP_LAB_PASS"),
		Roots: []string{root}, Depth: 6, ScanEvery: time.Hour, Mode: ModeStream, Connections: 4, MaxActive: 1, Retention: time.Hour}
	return s, media
}

// TestLabStreamsOverOpenSSH : vrai serveur OpenSSH (conteneur local).
func TestLabStreamsOverOpenSSH(t *testing.T) {
	s, media := labSettings(t, ProtoSFTP, "FTP_LAB_SFTP", "FTP_LAB_SFTP_ROOT")
	fp, err := ProbeHostKey(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if res := Test(context.Background(), s); res.OK || res.Fingerprint != fp {
		t.Fatalf("test sans empreinte : %+v", res)
	}
	s.HostKey = fp // laboratoire local : empreinte lue juste avant
	if res := Test(context.Background(), s); !res.OK {
		t.Fatalf("test : %+v", res)
	}
	labFlow(t, s, media)
}

// TestLabStreamsOverFTPS : vrai serveur vsftpd en FTPS explicite, certificat
// auto-signé épinglé par son empreinte.
func TestLabStreamsOverFTPS(t *testing.T) {
	s, media := labSettings(t, ProtoFTPS, "FTP_LAB_FTPS", "FTP_LAB_FTPS_ROOT")
	if res := Test(context.Background(), s); res.OK || res.Fingerprint == "" {
		t.Fatalf("certificat auto-signé accepté sans empreinte : %+v", res)
	}
	fp, err := ProbeCertificate(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	s.HostKey = fp
	if res := Test(context.Background(), s); !res.OK {
		t.Fatalf("test : %+v", res)
	}
	labFlow(t, s, media)
}
