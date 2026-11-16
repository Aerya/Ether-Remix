package discovery

import (
	"strings"
	"testing"
)

func TestPublicGalleryImages(t *testing.T) {
	acdb := publicImages([]byte(`<img src="/static/img/edit.svg"><img class="primary-img" src="/static/images/abc_default.jpg?v=1" alt="Netflix"><img class="primary-img" src="https://evil.test/a.jpg">`), "ACdb", "Netflix", "https://acdb.tv/library-posters/")
	if len(acdb) != 1 || acdb[0].Name != "Netflix" || !strings.HasPrefix(acdb[0].URL, "https://acdb.tv/static/images/") {
		t.Fatalf("%+v", acdb)
	}
	tpdb := publicImages([]byte(`<div data-poster-id="123"></div><source type="image/jpeg" srcset="https://images.theposterdb.com/prod/public/images/posters/optimized/a.jpg"><source type="image/webp" srcset="https://evil.test/a.webp">`), "ThePosterDB", "Pixar", "https://theposterdb.com/posters/12")
	if len(tpdb) != 1 || tpdb[0].Page != "https://theposterdb.com/posters/12" {
		t.Fatalf("%+v", tpdb)
	}
}

func TestMediuxOnlySelectedGenrePosters(t *testing.T) {
	data := []byte(`<img src="/_next/image?url=https%3A%2F%2Fapi.mediux.pro%2Fassets%2Funrelated"><script>self.__next_f.push([1,"1:[{\"genrePosters\":[{\"id\":\"3243c885-9635-405b-ad5e-0eb3059080e1\",\"title\":\"Action\"},{\"id\":\"../../bad\",\"title\":\"invalid\"}]}]"])</script>`)
	images := publicImages(data, "MediUX", "action", "https://mediux.pro/genres/28")
	if len(images) != 1 || images[0].Name != "Action" {
		t.Fatalf("unexpected images: %+v", images)
	}
}
