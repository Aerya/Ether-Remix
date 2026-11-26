package plex

import (
	"bytes"
	"context"
	"encoding/json"
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
	rows := stableHomeHubs(nil, []model.Hub{{Type: "movie", HubIdentifier: "ranked", RankedTop10: true, TotalSize: model.Ptr(100), Metadata: []model.Metadata{source}}})
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
					rows := heroReadyHomeHubs(stableHomeHubs(nil, []model.Hub{
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

// Plex Web and desktop/HTML apps drop server-supplied Image[type="coverArt"]
// during metadata normalization and resolve Meta.DisplayImage(coverArt) to an
// empty card. They must receive no coverArt DisplayImage so they render the
// ranked portrait composite from thumb, while iOS/tvOS/Android keep wide coverArt.
func TestRankedClientDisplayImageInvariant(t *testing.T) {
	source := model.Metadata{
		Type: "movie", RatingKey: "123", Key: "/library/metadata/123",
		Thumb: "/original/poster", Art: "/original/art",
		Image: []model.Image{{Type: "coverPoster", URL: "/original/poster"}},
	}
	cases := []struct {
		name         string
		pr           *Request
		wantCoverArt bool
	}{
		{"Plex for Mac", &Request{Product: "Plex for Mac", Platform: "OSX"}, false},
		{"Plex for Windows", &Request{Product: "Plex for Windows", Platform: "Windows"}, false},
		{"Plex for Linux", &Request{Product: "Plex for Linux", Platform: "Linux"}, false},
		{"Plex Desktop", &Request{Product: "Plex Desktop", Platform: "macOS"}, false},
		{"Plex Web", &Request{Product: "Plex Web", Platform: "Chrome"}, false},
		{"Plex HTPC", &Request{Product: "Plex HTPC for Mac", Platform: "OSX"}, false},
		{"Plex for Samsung", &Request{Product: "Plex for Samsung", Platform: "Tizen"}, false},
		{"Plex for iOS", &Request{Product: "Plex for iOS", Platform: "iOS"}, true},
		{"Plex iOS product", &Request{Product: "Plex", Platform: "iOS"}, true},
		{"Plex for Apple TV", &Request{Product: "Plex for Apple TV", Platform: "tvOS"}, true},
		{"Plex for Android", &Request{Product: "Plex for Android (Mobile)", Platform: "Android"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := stableHomeHubs(tc.pr, []model.Hub{{
				Type: "movie", HubIdentifier: "ranked", Style: "shelf",
				RankedTop10: true, Metadata: []model.Metadata{source},
			}})
			if len(rows) != 1 {
				t.Fatalf("hubs = %d, want 1", len(rows))
			}
			got := rows[0]
			hasCoverArtMeta := got.Meta != nil && len(got.Meta.DisplayImage) == 1 && got.Meta.DisplayImage[0].ImageType == "coverArt"
			if hasCoverArtMeta != tc.wantCoverArt {
				t.Fatalf("hub Meta.DisplayImage coverArt = %v, want %v (meta=%+v)", hasCoverArtMeta, tc.wantCoverArt, got.Meta)
			}
			if got.Style == "hero" || got.Metadata[0].Art != source.Art {
				t.Fatalf("hub altered style or art: style=%q art=%q", got.Style, got.Metadata[0].Art)
			}
			if !strings.Contains(got.Metadata[0].Thumb, "ranked=1") || strings.Contains(got.Metadata[0].Thumb, "rankshape=wide") {
				t.Fatalf("hub thumb = %q, want portrait ranked URL", got.Metadata[0].Thumb)
			}

			mc := &model.MediaContainer{Metadata: []model.Metadata{source}, TotalSize: model.Ptr(10)}
			paintRankedCollection(tc.pr, mc, 0)
			colCoverArtMeta := mc.Meta != nil && len(mc.Meta.DisplayImage) == 1 && mc.Meta.DisplayImage[0].ImageType == "coverArt"
			if colCoverArtMeta != tc.wantCoverArt {
				t.Fatalf("collection Meta.DisplayImage coverArt = %v, want %v (meta=%+v)", colCoverArtMeta, tc.wantCoverArt, mc.Meta)
			}
			if !strings.Contains(mc.Metadata[0].Thumb, "ranked=1") || strings.Contains(mc.Metadata[0].Thumb, "rankshape=wide") {
				t.Fatalf("collection thumb = %q, want portrait ranked URL", mc.Metadata[0].Thumb)
			}
		})
	}

	// End-to-end check on /hubs/promoted?contentDirectoryID=1 (the desktop/web
	// home request) and /photo/:/transcode for Plex for Mac vs Plex for iOS.
	f := newFixture()
	defer f.srv.Close()
	title := fightClub
	title.BackdropPath = "/backdrop.jpg"
	f.catalog.titles[title.Key] = title
	ov := overlays.New(discoveryDB(t), overlays.Options{CacheDir: t.TempDir()})
	f.srv.setDiscovery((&discovery.Features{Overlays: ov}).Frontend())
	f.srv.bindHost(frontend.Host{Artwork: FrontendArtwork{Server: f.srv}})
	f.srv.setRecommender(&fakeRecommender{home: []core.Hub{{
		ID: "home.top10", Title: "Top 10 Movies", Type: core.TypeMovie,
		Promoted: true, RankedTop10: true, Items: []core.Title{title},
	}},
	})

	fetchHub := func(product, platform string) model.Hub {
		t.Helper()
		rec := f.get(t, "/hubs/promoted?contentDirectoryID=1&pinnedContentDirectoryID=1",
			"Accept", "application/json",
			"X-Plex-Product", product,
			"X-Plex-Platform", platform,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s /hubs/promoted: %d %s", product, rec.Code, rec.Body.String())
		}
		var resp struct {
			MediaContainer struct {
				Hub []model.Hub `json:"Hub"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		for _, h := range resp.MediaContainer.Hub {
			if strings.HasPrefix(h.HubIdentifier, "home.top10") {
				return h
			}
		}
		t.Fatalf("%s: movie.top10 hub not found in %+v", product, resp.MediaContainer.Hub)
		return model.Hub{}
	}

	macHub := fetchHub("Plex for Mac", "OSX")
	if macHub.Meta != nil {
		t.Fatalf("Plex for Mac got Meta=%+v, want nil", macHub.Meta)
	}
	macImg := f.get(t, "/photo/:/transcode?width=240&height=360&minSize=1&upscale=1&url="+url.QueryEscape(macHub.Metadata[0].Thumb),
		"X-Plex-Product", "Plex for Mac", "X-Plex-Platform", "OSX")
	if macImg.Code != http.StatusOK {
		t.Fatalf("Plex for Mac transcode thumb: %d %s", macImg.Code, macImg.Body.String())
	}
	macRaster, _, err := image.Decode(bytes.NewReader(macImg.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if macRaster.Bounds().Dy() <= macRaster.Bounds().Dx() {
		t.Fatalf("Plex for Mac ranked thumb dimensions = %v, want portrait", macRaster.Bounds())
	}

	iosHub := fetchHub("Plex for iOS", "iOS")
	if iosHub.Meta == nil || len(iosHub.Meta.DisplayImage) == 0 || iosHub.Meta.DisplayImage[0].ImageType != "coverArt" {
		t.Fatalf("Plex for iOS Meta = %+v, want coverArt DisplayImage", iosHub.Meta)
	}
	var coverArtURL string
	for _, img := range iosHub.Metadata[0].Image {
		if img.Type == "coverArt" {
			coverArtURL = img.URL
		}
	}
	if coverArtURL == "" {
		t.Fatalf("Plex for iOS missing coverArt image: %+v", iosHub.Metadata[0].Image)
	}
	iosImg := f.get(t, "/photo/:/transcode?width=480&url="+url.QueryEscape(coverArtURL),
		"X-Plex-Product", "Plex for iOS", "X-Plex-Platform", "iOS")
	if iosImg.Code != http.StatusOK {
		t.Fatalf("Plex for iOS transcode coverArt: %d %s", iosImg.Code, iosImg.Body.String())
	}
	iosRaster, _, err := image.Decode(bytes.NewReader(iosImg.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if iosRaster.Bounds().Dx() <= iosRaster.Bounds().Dy() {
		t.Fatalf("Plex for iOS ranked coverArt dimensions = %v, want wide", iosRaster.Bounds())
	}
}
