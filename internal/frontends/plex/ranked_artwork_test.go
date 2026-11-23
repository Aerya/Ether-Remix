package plex

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/discovery"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
	"github.com/braedonsaunders/ether/internal/overlays"
)

func TestRankedHubNativeTypesAndIsolation(t *testing.T) {
	source := model.Metadata{Type: "movie", RatingKey: "123", Key: "/library/metadata/123", Thumb: "/original/poster", Art: "/original/art", Image: []model.Image{{Type: "coverPoster", URL: "/original/poster"}}}
	rows := stableHomeHubs([]model.Hub{{Type: "movie", HubIdentifier: "ranked", RankedTop10: true, TotalSize: model.Ptr(100), Metadata: []model.Metadata{source}}})
	if len(rows) != 1 || rows[0].TotalSize == nil || *rows[0].TotalSize != 1 || rows[0].Meta == nil || rows[0].Meta.DisplayImage[0].ImageType != "coverArt" {
		t.Fatal(rows)
	}
	got := rows[0].Metadata[0]
	if got.Type != source.Type || got.Key != source.Key || got.Art != source.Art || !strings.Contains(got.Thumb, "ranked=1") {
		t.Fatal(got)
	}
	if source.Image[0].URL != "/original/poster" {
		t.Fatal("mutated ordinary art")
	}
	found := false
	for _, img := range got.Image {
		if img.Type == "coverArt" && strings.Contains(img.URL, "rankshape=wide") {
			found = true
		}
	}
	if !found {
		t.Fatal("no wide image")
	}
}

// Home paints canonical metadata, whereas collection children carry a scoped
// URL. Both must retain authorization across rank variants and photo transcode.
func TestRankedHomeArtwork(t *testing.T) {
	for _, future := range []bool{false, true} {
		for _, preview := range []bool{false, true} {
			for _, backdrop := range []string{"/backdrop.jpg", "", "/missing.jpg"} {
				t.Run(fmt.Sprintf("future=%t/preview=%t/backdrop=%s", future, preview, backdrop), func(t *testing.T) {
					f := newFixture()
					defer f.srv.Close()
					title := fightClub
					title.BackdropPath = backdrop
					if future {
						title.Released = fixedNow.Add(48 * time.Hour)
					}
					f.catalog.titles[title.Key] = title
					ck := core.SmartKey(991)
					cols := &scopedFakeCollections{&fakeCollections{cols: map[core.RatingKey]core.Collection{ck: {Key: ck, Type: core.TypeMovie, Upcoming: future}}, items: map[core.RatingKey][]core.Title{ck: {title}}}}
					f.srv.setCollections(cols)
					ov := overlays.New(discoveryDB(t), overlays.Options{CacheDir: t.TempDir()})
					f.srv.setDiscovery((&discovery.Features{Overlays: ov}).Frontend())
					f.srv.bindHost(frontend.Host{Artwork: FrontendArtwork{Server: f.srv}})
					pr := &Request{User: &ownerUser, Product: "Plex Web", Platform: "Chrome"}
					md := f.srv.metadata(context.Background(), pr, &title, renderOpts{})
					rows := heroReadyHomeHubs(stableHomeHubs([]model.Hub{
						{HubIdentifier: "continue", Metadata: []model.Metadata{{Type: "movie", Thumb: "/poster", Art: "/art"}}},
						{HubIdentifier: "collection", Type: "movie", RankedTop10: true, PreviewOnFocus: preview, Metadata: []model.Metadata{md}},
					}))
					if len(rows) != 2 || rows[1].Style == "hero" {
						t.Fatal("ranked shelf became hero", rows)
					}
					for _, scope := range []bool{false, true} {
						item := rows[1].Metadata[0]
						if scope {
							scoped := f.srv.placeholderMetadata(context.Background(), pr, &title, ck)
							items := []model.Metadata{scoped}
							rankedMetadata(items)
							item = items[0]
						}
						for _, img := range item.Image {
							if img.Type != "coverPoster" && img.Type != "coverArt" {
								continue
							}
							for _, product := range []string{"Plex Web", "Plex for Apple TV"} {
								for _, wrapped := range []bool{false, true} {
									target := img.URL
									if wrapped {
										target = "/photo/:/transcode?width=240&url=" + url.QueryEscape(target)
									}
									rec := f.get(t, target, "X-Plex-Product", product)
									if rec.Code != http.StatusOK {
										t.Fatalf("%s scoped=%t %s: %d %s", product, scope, target, rec.Code, rec.Body.String())
									}
									raster, _, err := image.Decode(bytes.NewReader(rec.Body.Bytes()))
									if err != nil {
										t.Fatal(err)
									}
									wide := img.Type == "coverArt" && backdrop != "" && backdrop != "/missing.jpg"
									bounds := raster.Bounds()
									if (bounds.Dx() > bounds.Dy()) != wide {
										t.Fatalf("wide=%t dimensions=%v", wide, bounds)
									}
									// A successful response must contain painted artwork, not a black tile.
									r, g, b, _ := raster.At(bounds.Max.X*3/4, bounds.Max.Y/2).RGBA()
									if r+g+b < 10000 {
										t.Fatal("blank ranked artwork")
									}
									if denied := f.get(t, target, "X-Plex-Product", product, "X-Plex-Token", friendToken); future && denied.Code != 404 {
										t.Fatalf("placeholder leaked: %d", denied.Code)
									}
								}
							}
						}
					}
				})
			}
		}
	}
}
