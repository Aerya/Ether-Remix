package discovery

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

// Opt-in only: normal tests never contact third-party catalogues.
func TestLivePublicGalleries(t *testing.T) {
	if os.Getenv("REMIX_TEST_PUBLIC_GALLERIES") != "1" {
		t.Skip("opt-in network test")
	}
	for _, c := range []struct{ provider, q, group string }{{"ACdb", "netflix", "services"}, {"ThePosterDB", "netflix", "services"}, {"MediUX", "action", "genres"}} {
		t.Run(c.provider, func(t *testing.T) {
			images, err := loadPublicGallery(context.Background(), c.provider, c.q, c.group, "28")
			if err != nil {
				t.Fatal(err)
			}
			if len(images) == 0 {
				t.Fatal("no images")
			}
			t.Log(images[0].URL)
			req, _ := http.NewRequest("GET", images[0].URL, nil)
			req.Header.Set("User-Agent", "Ether (+https://github.com/braedonsaunders/ether)")
			req.Header.Set("Range", "bytes=0-1023")
			res, err := galleryClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if (res.StatusCode != 200 && res.StatusCode != 206) || !strings.HasPrefix(res.Header.Get("Content-Type"), "image/") {
				t.Fatalf("image not accessible: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
			}
			t.Logf("%d selectable images; first image HTTP %d", len(images), res.StatusCode)
		})
	}
}
