package artwork

import (
	"bytes"
	"context"
	"image"
	"testing"
)

func TestNavigationCardLandscapeFallbackAndCacheIdentity(t *testing.T) {
	r := NewRenderer(nil, t.TempDir(), nil)
	req := PosterRequest{Title: "Studio Ghibli · Films", Width: 600, Navigation: true, Accent: "#159fbe"}
	data, _, err := r.Poster(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != 600 || img.Bounds().Dy() != 337 {
		t.Fatal(img.Bounds(), err)
	}
	req.Navigation = false
	portrait, _, err := r.Poster(context.Background(), req)
	if err != nil || bytes.Equal(data, portrait) {
		t.Fatal("card and poster cache collided", err)
	}
}
