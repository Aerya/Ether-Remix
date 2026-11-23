package onair

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"
)

func TestLibraryExcludesReelAssetsAndServesLinkPosters(t *testing.T) {
	var posterPNG bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 64, 36))
	for y := 0; y < 36; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: 40, G: 120, B: 200, A: 255})
		}
	}
	if err := png.Encode(&posterPNG, img); err != nil {
		t.Fatal(err)
	}

	archive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/services/img/retro-spot-1989" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(posterPNG.Bytes())
			return
		}
		http.NotFound(w, r)
	}))
	defer archive.Close()

	s := testService(t, Options{
		Showcase:      true,
		ArchiveOrigin: archive.URL,
		HTTPClient:    archive.Client(),
	})
	ctx := context.Background()

	reel, err := s.SaveReel(ctx, Reel{Name: "Test Ads", Order: "shuffle", RepeatHours: 1})
	if err != nil {
		t.Fatal(err)
	}
	clip, err := s.AddLinkClip(ctx, reel.ID, LinkClipInput{
		Name:       "Retro Spot 1989",
		Source:     archive.URL + "/details/retro-spot-1989",
		DurationMS: int64((30 * time.Second) / time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.putAsset(ctx, "station-mark.png", "image", "", "image/png", posterPNG.Bytes()); err != nil {
		t.Fatal(err)
	}

	h := s.Handler()

	// GET /assets must exclude kind='reel' rows while returning LibraryAsset entries.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets status = %d: %s", rec.Code, rec.Body.String())
	}
	var listed []LibraryAsset
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode LibraryAsset list: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "station-mark.png" {
		t.Fatalf("expected only the 1 non-reel library asset, got %+v", listed)
	}

	// GET /assets/{sourceDigest}/thumb serves a JPEG poster for uncached Archive.org link clips.
	thumbRec := httptest.NewRecorder()
	h.ServeHTTP(thumbRec, httptest.NewRequest(http.MethodGet, "/assets/"+clip.SourceDigest+"/thumb", nil))
	if thumbRec.Code != http.StatusOK {
		t.Fatalf("GET /assets/%s/thumb status = %d: %s", clip.SourceDigest, thumbRec.Code, thumbRec.Body.String())
	}
	if ct := thumbRec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("expected image/jpeg, got %q", ct)
	}
	if thumbRec.Body.Len() == 0 {
		t.Fatal("expected non-empty JPEG bytes")
	}

	// If ffmpeg is present, verify cached link clip local MP4 extraction too.
	if ff, err := exec.LookPath("ffmpeg"); err == nil {
		s2 := testService(t, Options{
			FFmpeg: func() string { return ff },
		})
		r2, err := s2.SaveReel(ctx, Reel{Name: "Cached Reel", Order: "shuffle", RepeatHours: 1})
		if err != nil {
			t.Fatal(err)
		}
		c2, err := s2.AddLinkClip(ctx, r2.ID, LinkClipInput{
			Name:       "Cached Clip",
			Source:     "https://cdn.example.com/spot.mp4",
			DurationMS: 2000,
		})
		if err != nil {
			t.Fatal(err)
		}
		mp4Path := t.TempDir() + "/spot.mp4"
		cmd := exec.CommandContext(ctx, ff, "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=navy:s=320x180:d=1", "-pix_fmt", "yuv420p", "-y", mp4Path)
		if err := cmd.Run(); err != nil {
			t.Fatalf("generate test mp4: %v", err)
		}
		w, hgt := s2.workingSize()
		key := linkCacheKey(c2.Source, w, hgt, r2.LoudnessMatch, r2.TrimSilence)
		s2.mu.Lock()
		s2.linkCache[key] = linkEntry{Path: mp4Path, Size: 1024, Duration: time.Second}
		s2.mu.Unlock()

		rec2 := httptest.NewRecorder()
		s2.Handler().ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/assets/"+c2.SourceDigest+"/thumb", nil))
		if rec2.Code != http.StatusOK || rec2.Body.Len() == 0 {
			t.Fatalf("cached link clip thumb failed: code=%d len=%d body=%s", rec2.Code, rec2.Body.Len(), rec2.Body.String())
		}
	}
}
