package app

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/artwork"
	artworkservice "github.com/braedonsaunders/ether/internal/artwork/service"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// newSharedArtwork composes domain services without constructing a protocol server.
func (a *App) newSharedArtwork(ctx context.Context) (*artworkservice.Source, func()) {
	now := parityClock()
	if now == nil {
		now = time.Now
	}
	imageURL := a.tmdb.ImageURLFunc()
	paletteDB := a.db
	if a.spikeLibrary != nil {
		imageURL = func(path, size string) string { return "https://image.tmdb.org/t/p/" + size + path }
		paletteDB = nil
	}
	var images *artworkservice.RemoteCache
	if a.images != nil {
		images = artworkservice.NewBackedRemoteCache(128<<20, a.images)
	} else {
		images = artworkservice.NewRemoteCache(128<<20, egress.Client(egress.CapMetadata, 20*time.Second))
	}
	var workers sync.WaitGroup
	queue := artworkservice.NewQueue(ctx, &workers, 2, 512)
	collections := a.discoveryPersonal.ReviewCollections(a.collections)
	var collectionArt artworkservice.CollectionArt
	if art, ok := collections.(artworkservice.CollectionArt); ok {
		collectionArt = art
	}
	palettes := artworkservice.NewPaletteCache(paletteDB, queue.Submit, now, a.log, func(ctx context.Context, key core.RatingKey, path string) (artworkservice.Palette, error) {
		return artworkservice.ComputeItemPalette(ctx, images, imageURL, key, path, now(), collectionArt)
	})
	visibility := experienceVisibility(a.safety, a.catalog.StoredTitle, func(ctx context.Context, titles []core.Title) []core.Title {
		if a.availability == nil {
			return titles
		}
		return a.availability.FilterDiscovery(ctx, titles)
	}, func() []*core.PlaySession {
		if a.playback == nil {
			return nil
		}
		return a.playback.Sessions()
	}, []experience.Library{{ID: 1, Type: core.TypeMovie}, {ID: 2, Type: core.TypeShow}})
	source := &artworkservice.Source{OwnerEnabled: func() bool { enabled := a.cfg.Read().Posters.OwnerEnabled; return enabled == nil || *enabled }, PosterGen: func() uint64 { return a.cfg.Read().Posters.Generation }, Assets: &artwork.LocalAssets{}, AssetFolder: func() string { return a.cfg.Read().Posters.AssetFolder }, OwnerDir: filepath.Join(a.opts.DataDir, "artwork", "owner"), Catalog: pluginArtworkTitles{catalog: a.catalog, app: a}, Collections: collections, Playlists: a.playlists, Visibility: visibility, Discovery: a.discoveryFeatures.Frontend(), Images: images, ImageURL: imageURL, Palettes: palettes, Now: now, Raster: artwork.NewSVGRasterizer()}
	if a.livetv != nil {
		source.LiveTV = a.livetv
	}
	if a.spikeLibrary != nil {
		source.Catalog, _ = a.spikeLibrary.Catalog().(core.StoredTitleLookup)
		source.Collections = a.spikeLibrary.Collections()
		source.Playlists = a.spikeLibrary.Playlists()
		source.Visibility = experienceVisibility(a.safety, nil, nil, a.spikeLibrary.Playback().Sessions, serverLibraries(a.cfg.Read()))
	}
	if a.episodeorder != nil {
		source.VirtualSeason = a.episodeorder.CachedVirtualSeason
	}
	if a.opts.urlImages != nil {
		source.URLImages = a.opts.urlImages
	} else if a.images != nil {
		source.URLImages = a.images
	}
	source.PluginItem = a.sharedPluginArtworkItem
	source.LibraryLiveThumb = func(ctx context.Context, viewer *core.Viewer, viewID string, path string) (string, error) {
		return surfaceLibraryLive{app: a}.AuthorizeLibraryLiveThumb(ctx, viewer, viewID, path)
	}
	source.ViewerAvatar = func(ctx context.Context, viewer *core.Viewer) ([]byte, string, error) {
		if viewer == nil || viewer.ID <= 0 || viewer.PlexID <= 0 || strings.TrimSpace(viewer.Thumb) == "" {
			return nil, "", core.ErrNotFound
		}
		token := ""
		if a.cfg != nil {
			token = strings.TrimSpace(a.cfg.Read().Plex.Token)
		}
		return a.cachedPlexAvatar(ctx, token, viewer.Thumb)
	}
	variants := artworkservice.NewVariantsQueue(ctx, a.tmdb.CachedArtworkVariants, a.tmdb.ArtworkVariants)
	source.Variants = variants.Get
	source.Generated = a.titlePosters()
	source.Collages = &artworkservice.LibraryCollageCache{Dir: filepath.Join(a.opts.DataDir, "cache", "library-collages")}
	// source.Read binds a copy of source: everything Read uses is set above.
	metadata := artworkservice.NewMetadataQueue(ctx, a.db, a.users.ActiveUser, source.Read)
	if a.spikeLibrary == nil {
		// Search and list hits with no row keep their artwork facts in
		// memory: a search page writes nothing.
		persists := func(ctx context.Context, key core.RatingKey) bool {
			if key.IsPluginKey() {
				return false
			}
			return a.catalog.PersistsArtwork(ctx, key)
		}
		metadata.PersistFor(persists)
		palettes.PersistFor(persists)
	}
	source.Metadata = metadata.Get
	source.BindMetadata(metadata)
	return source, func() { variants.Wait(); metadata.Wait(); workers.Wait() }
}

func (a *App) sharedPluginArtworkItem(ctx context.Context, viewer *core.User, _ core.Device, key core.RatingKey) (*core.Title, []frontend.Image, error) {
	if a == nil || viewer == nil || !key.IsPluginKey() {
		return nil, nil, core.ErrUnauthorized
	}
	active := viewer
	if a.users != nil {
		resolved, err := a.users.PolicyUser(ctx, viewer.ID)
		if err != nil || resolved == nil || resolved.ID != viewer.ID {
			return nil, nil, core.ErrUnauthorized
		}
		active = resolved
	}
	if a.pluginsRT == nil || a.pluginsRT.kindCatalog == nil {
		return nil, nil, core.ErrNotFound
	}
	piv, err := a.pluginsRT.kindCatalog.Item(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	if piv.Library.Hidden || !a.pluginsRT.kindCatalog.IsPluginLibraryActive(ctx, piv.Library) {
		return nil, nil, core.ErrNotFound
	}
	if !experience.SectionAllowed(active, int(piv.Library.ID)) || !experience.TitleAllowed(a.safety, active, &piv.Title) {
		return nil, nil, core.ErrNotFound
	}
	t := piv.Title
	return &t, piv.Images, nil
}

// serverLibraries preserves the canonical section IDs/order without a Plex server.
func serverLibraries(cfg config.Config) []experience.Library {
	out := []experience.Library{{ID: 1, Name: "Movies", Type: core.TypeMovie}}
	if !cfg.Spike.Enabled {
		out = append(out, experience.Library{ID: 2, Name: "TV Shows", Type: core.TypeShow})
	}
	return out
}

// titlePosters draws Ether's generated poster for a title with no artwork.
// It draws only type, so it needs no image source; its disk cache keeps
// each title's image, so a page of fallbacks is drawn once.
func (a *App) titlePosters() *artwork.Renderer {
	a.titlePosterOnce.Do(func() {
		dir := ""
		if a.opts.DataDir != "" {
			dir = filepath.Join(a.opts.DataDir, "cache", "title-posters")
		}
		a.titlePosterRenderer = artwork.NewRenderer(nil, dir, a.log.With("component", "artwork"))
	})
	return a.titlePosterRenderer
}

// pluginArtworkTitles lets the shared artwork reader find plugin library
// items, whose keys are not in the catalogue. The viewer's grant for the
// image was issued by the experience layer, which already applied library
// access to the item.
type pluginArtworkTitles struct {
	catalog core.StoredTitleLookup
	app     *App
}

func (p pluginArtworkTitles) StoredTitle(ctx context.Context, key core.RatingKey) (*core.Title, error) {
	if key.IsPluginKey() {
		if p.app == nil || p.app.pluginsRT == nil || p.app.pluginsRT.kindCatalog == nil {
			return nil, core.ErrNotFound
		}
		piv, err := p.app.pluginsRT.kindCatalog.Item(ctx, key)
		if err != nil {
			return nil, err
		}
		t := piv.Title
		return &t, nil
	}
	return p.catalog.StoredTitle(ctx, key)
}
