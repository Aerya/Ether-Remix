package libraries

import (
	"fmt"
	"maps"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// KindPresentationLevel describes the shape facts of one hierarchy level
// needed to infer per-app library presentations.
type KindPresentationLevel struct {
	Key       string
	Parent    string // optional earlier level key; empty defaults to previous level
	Media     string // optional core.MediaClass override for this level
	Role      string // optional free-text semantic role
	Playable  bool
	Container bool
	// Members: a list level (a playlist), a root of its own whose children
	// are other levels' items.
	Members bool
}

// KindPresentationInput supplies the kind-level facts (class, levels, optional
// manifest presentation overrides/defaults) used by ResolveKindPresentations.
type KindPresentationInput struct {
	Media   string // optional core.MediaClass
	Levels  []KindPresentationLevel
	Present core.Presentations
}

// AppSwitchPatch carries per-app owner publish switch updates from the console.
type AppSwitchPatch struct {
	AdapterOnly  *bool `json:"adapterOnly,omitempty"`
	Vela         *bool `json:"vela,omitempty"`
	Plex         *bool `json:"plex,omitempty"`
	MediaBrowser *bool `json:"mediaBrowser,omitempty"`
	Stremio      *bool `json:"stremio,omitempty"`
	Xtream       *bool `json:"xtream,omitempty"`
	Silo         *bool `json:"silo,omitempty"`
}

const (
	reasonPlexBook      = "Plex does not have a book library type."
	reasonPlexAudiobook = "Plex does not have an audiobook library type."
	reasonPlexPodcast   = "Plex does not have a podcast library type."
	reasonStremioVideo  = "Stremio only supports movies, shows, and channels."
	reasonXtreamVideo   = "Xtream only supports movies, series, and live TV."
	reasonSiloVideo     = "Silo only supports movie and TV show libraries."
)

// ResolveKindPresentations computes the complete presentation picture for a
// plugin content kind:
//   - MediaClass and per-level MediaRole are inferred in strict priority:
//     1. explicit kind.media / level.role
//     2. declared present hints (mediaBrowser.collectionType/itemTypes, plex.base)
//     3. level shape (keys, depth, playability)
//   - Available holds HOW each fitting app sees the library (manifest override
//     when declared, otherwise inferred from the kind's class/roles/shape).
//   - Effective holds ONLY the currently published apps, combining Available
//     with manifest default hints and the owner's sticky PresentOverrides.
func ResolveKindPresentations(in KindPresentationInput, overrides core.PresentationOverrides) core.ResolvedPresentations {
	class := inferMediaClass(in)
	roles := inferLevelRoles(class, in)
	multiLevel := len(in.Levels) >= 2

	adapterOnly := in.Present.AdapterOnly
	if overrides.AdapterOnly != nil {
		adapterOnly = *overrides.AdapterOnly
	}

	var avail core.Presentations
	apps := make([]core.ResolvedAppPresentation, 0, 6)

	// 1. Vela — supports all media classes.
	{
		app := core.ResolvedAppPresentation{
			App:       "vela",
			Available: true,
			DefaultOn: defaultFlag(in.Present.Vela != nil && in.Present.Vela.Default != nil, ptrBoolVal(velaDefault(in.Present.Vela))),
		}
		if in.Present.Vela != nil && strings.TrimSpace(in.Present.Vela.Type) != "" {
			t := strings.TrimSpace(in.Present.Vela.Type)
			avail.Vela = &core.VelaPresentation{Type: t}
			app.Source = core.PresentationSourcePlugin
			app.HintValue = t
		} else {
			t := inferVelaKindType(class, in.Levels, in.Present)
			avail.Vela = &core.VelaPresentation{Type: t}
			app.Source = core.PresentationSourceInferred
			app.HintValue = t
		}
		apps = append(apps, app)
	}

	// 2. Plex — supports video (movie|show), music (artist), photo (photo).
	{
		app := core.ResolvedAppPresentation{
			App:       "plex",
			DefaultOn: defaultFlag(in.Present.Plex != nil && in.Present.Plex.Default != nil, ptrBoolVal(plexDefault(in.Present.Plex))),
		}
		if in.Present.Plex != nil && strings.TrimSpace(in.Present.Plex.Base) != "" {
			base := strings.TrimSpace(in.Present.Plex.Base)
			avail.Plex = &core.PlexPresentation{Base: base}
			app.Available = true
			app.Source = core.PresentationSourcePlugin
			app.HintValue = base
		} else if base, reason := inferPlexBase(class, multiLevel); base != "" {
			avail.Plex = &core.PlexPresentation{Base: base}
			app.Available = true
			app.Source = core.PresentationSourceInferred
			app.HintValue = base
		} else {
			app.Available = false
			app.DefaultOn = false
			app.Source = core.PresentationSourceUnavailable
			app.Reason = reason
		}
		apps = append(apps, app)
	}

	// 3. Jellyfin & Emby (MediaBrowser) — supports all media classes.
	{
		app := core.ResolvedAppPresentation{
			App:       "mediaBrowser",
			Available: true,
			DefaultOn: defaultFlag(in.Present.MediaBrowser != nil && in.Present.MediaBrowser.Default != nil, ptrBoolVal(mbDefault(in.Present.MediaBrowser))),
		}
		if in.Present.MediaBrowser != nil && strings.TrimSpace(in.Present.MediaBrowser.CollectionType) != "" {
			ct := strings.TrimSpace(in.Present.MediaBrowser.CollectionType)
			// Fill any unmapped levels so Jellyfin never falls back to Movie/Series
			// for music, book, audiobook, or photo libraries with partial itemTypes.
			mbClass := mediaClassFromMBCollection(ct, class)
			itemTypes := inferMediaBrowserItemTypes(mbClass, in.Levels, roles, in.Present.MediaBrowser.ItemTypes)
			avail.MediaBrowser = &core.MediaBrowserPresentation{
				CollectionType: ct,
				ItemTypes:      itemTypes,
			}
			app.Source = core.PresentationSourcePlugin
			app.HintValue = ct
		} else {
			ct := inferMediaBrowserCollectionType(class, multiLevel)
			var existing map[string]string
			if in.Present.MediaBrowser != nil {
				existing = in.Present.MediaBrowser.ItemTypes
			}
			itemTypes := inferMediaBrowserItemTypes(class, in.Levels, roles, existing)
			avail.MediaBrowser = &core.MediaBrowserPresentation{
				CollectionType: ct,
				ItemTypes:      itemTypes,
			}
			app.Source = core.PresentationSourceInferred
			app.HintValue = ct
		}
		apps = append(apps, app)
	}

	// 4. Stremio — supports video (movie|series) unless plugin explicitly declares.
	{
		app := core.ResolvedAppPresentation{
			App:       "stremio",
			DefaultOn: defaultFlag(in.Present.Stremio != nil && in.Present.Stremio.Default != nil, ptrBoolVal(stremioDefault(in.Present.Stremio))),
		}
		if in.Present.Stremio != nil && strings.TrimSpace(in.Present.Stremio.Type) != "" {
			st := strings.TrimSpace(in.Present.Stremio.Type)
			avail.Stremio = &core.StremioPresentation{Type: st}
			app.Available = true
			app.Source = core.PresentationSourcePlugin
			app.HintValue = st
		} else if class == core.MediaClassVideo {
			st := "movie"
			if multiLevel {
				st = "series"
			}
			avail.Stremio = &core.StremioPresentation{Type: st}
			app.Available = true
			app.Source = core.PresentationSourceInferred
			app.HintValue = st
		} else {
			app.Available = false
			app.DefaultOn = false
			app.Source = core.PresentationSourceUnavailable
			app.Reason = reasonStremioVideo
		}
		apps = append(apps, app)
	}

	// 5. Xtream — supports video (movie|series) unless plugin explicitly declares.
	{
		app := core.ResolvedAppPresentation{
			App:       "xtream",
			DefaultOn: defaultFlag(in.Present.Xtream != nil && in.Present.Xtream.Default != nil, ptrBoolVal(xtreamDefault(in.Present.Xtream))),
		}
		if in.Present.Xtream != nil && strings.TrimSpace(in.Present.Xtream.Category) != "" {
			cat := strings.TrimSpace(in.Present.Xtream.Category)
			avail.Xtream = &core.XtreamPresentation{Category: cat}
			app.Available = true
			app.Source = core.PresentationSourcePlugin
			app.HintValue = cat
		} else if class == core.MediaClassVideo {
			cat := "movie"
			if multiLevel {
				cat = "series"
			}
			avail.Xtream = &core.XtreamPresentation{Category: cat}
			app.Available = true
			app.Source = core.PresentationSourceInferred
			app.HintValue = cat
		} else {
			app.Available = false
			app.DefaultOn = false
			app.Source = core.PresentationSourceUnavailable
			app.Reason = reasonXtreamVideo
		}
		apps = append(apps, app)
	}

	// 6. Silo — supports video (movies|tvshows) unless plugin explicitly declares.
	{
		app := core.ResolvedAppPresentation{
			App:       "silo",
			DefaultOn: defaultFlag(in.Present.Silo != nil && in.Present.Silo.Default != nil, ptrBoolVal(siloDefault(in.Present.Silo))),
		}
		if in.Present.Silo != nil && strings.TrimSpace(in.Present.Silo.Type) != "" {
			st := strings.TrimSpace(in.Present.Silo.Type)
			avail.Silo = &core.SiloPresentation{Type: st}
			app.Available = true
			app.Source = core.PresentationSourcePlugin
			app.HintValue = st
		} else if class == core.MediaClassVideo {
			st := "movies"
			if multiLevel {
				st = "tvshows"
			}
			avail.Silo = &core.SiloPresentation{Type: st}
			app.Available = true
			app.Source = core.PresentationSourceInferred
			app.HintValue = st
		} else {
			app.Available = false
			app.DefaultOn = false
			app.Source = core.PresentationSourceUnavailable
			app.Reason = reasonSiloVideo
		}
		apps = append(apps, app)
	}

	var eff core.Presentations
	if adapterOnly {
		eff.AdapterOnly = true
	}
	for i := range apps {
		app := &apps[i]
		if !app.Available {
			app.Enabled = false
			continue
		}
		ov := overrides.Get(app.App)
		if ov != nil {
			app.Overridden = true
		}
		if adapterOnly {
			app.Enabled = false
			continue
		}
		if ov != nil {
			app.Enabled = *ov
		} else {
			app.Enabled = app.DefaultOn
		}
		if app.Enabled {
			copyAppPresentation(&eff, avail, app.App)
		}
	}

	var levelRoles map[string]core.MediaRole
	if len(in.Levels) > 0 && len(roles) == len(in.Levels) {
		levelRoles = make(map[string]core.MediaRole, len(in.Levels))
		for i, lv := range in.Levels {
			if lv.Key != "" && roles[i] != "" {
				levelRoles[lv.Key] = roles[i]
			}
		}
	}

	return core.ResolvedPresentations{
		MediaClass:  class,
		LevelRoles:  levelRoles,
		AdapterOnly: adapterOnly,
		Effective:   eff,
		Available:   avail,
		Apps:        apps,
	}
}

// ResolveCataloguePresentations builds the same ResolvedPresentations view for
// built-in, catalogue, and collection libraries (always video: movie or show).
func ResolveCataloguePresentations(mediaType core.MediaType, stored core.Presentations, overrides core.PresentationOverrides, isBuiltin bool) core.ResolvedPresentations {
	isShow := mediaType == core.TypeShow
	velaType, plexBase, mbType, stremioType, xtreamCat, siloType := "movies", "movie", "movies", "movie", "movie", "movies"
	if isShow {
		velaType, plexBase, mbType, stremioType, xtreamCat, siloType = "shows", "show", "tvshows", "series", "series", "tvshows"
	}
	avail := core.Presentations{
		Vela:         &core.VelaPresentation{Type: velaType},
		Plex:         &core.PlexPresentation{Base: plexBase},
		MediaBrowser: &core.MediaBrowserPresentation{CollectionType: mbType},
		Stremio:      &core.StremioPresentation{Type: stremioType},
		Xtream:       &core.XtreamPresentation{Category: xtreamCat},
		Silo:         &core.SiloPresentation{Type: siloType},
	}

	type spec struct {
		app   string
		hint  string
		hasOn bool
	}
	specs := []spec{
		{"vela", velaType, stored.Vela != nil},
		{"plex", plexBase, stored.Plex != nil},
		{"mediaBrowser", mbType, stored.MediaBrowser != nil},
		{"stremio", stremioType, stored.Stremio != nil},
		{"xtream", xtreamCat, stored.Xtream != nil},
		{"silo", siloType, stored.Silo != nil},
	}

	adapterOnly := stored.AdapterOnly
	if overrides.AdapterOnly != nil {
		adapterOnly = *overrides.AdapterOnly
	}

	var eff core.Presentations
	if adapterOnly && !isBuiltin {
		eff.AdapterOnly = true
	}
	apps := make([]core.ResolvedAppPresentation, 0, len(specs))
	for _, sp := range specs {
		ov := overrides.Get(sp.app)
		enabled := sp.hasOn
		if isBuiltin {
			enabled = true
		} else if adapterOnly {
			enabled = false
		} else if ov != nil {
			enabled = *ov
		}
		if enabled {
			copyAppPresentation(&eff, avail, sp.app)
		}
		apps = append(apps, core.ResolvedAppPresentation{
			App:        sp.app,
			Available:  true,
			Enabled:    enabled,
			DefaultOn:  isBuiltin,
			Overridden: ov != nil,
			Source:     core.PresentationSourceInferred,
			HintValue:  sp.hint,
		})
	}
	return core.ResolvedPresentations{
		MediaClass:  core.MediaClassVideo,
		AdapterOnly: adapterOnly && !isBuiltin,
		Effective:   eff,
		Available:   avail,
		Apps:        apps,
	}
}

// ApplyAppSwitches applies an owner's per-app switch changes on top of a
// resolved presentation, updating overrides stickily and computing the fresh
// effective Presentations (carrying manifest/inferred HOW for every enabled
// app). Attempting to enable an unavailable app returns ErrInvalid with its
// plain-language explanation.
func ApplyAppSwitches(resolved core.ResolvedPresentations, overrides core.PresentationOverrides, patch AppSwitchPatch) (core.Presentations, core.PresentationOverrides, error) {
	byApp := make(map[string]core.ResolvedAppPresentation, len(resolved.Apps))
	for _, a := range resolved.Apps {
		byApp[a.App] = a
	}

	nextOv := overrides
	if patch.AdapterOnly != nil {
		nextOv.Set("adapterOnly", patch.AdapterOnly)
	}
	adapterOnly := resolved.AdapterOnly
	if nextOv.AdapterOnly != nil {
		adapterOnly = *nextOv.AdapterOnly
	}

	patches := []struct {
		app string
		val *bool
	}{
		{"vela", patch.Vela},
		{"plex", patch.Plex},
		{"mediaBrowser", patch.MediaBrowser},
		{"stremio", patch.Stremio},
		{"xtream", patch.Xtream},
		{"silo", patch.Silo},
	}
	for _, p := range patches {
		if p.val == nil {
			continue
		}
		info, ok := byApp[p.app]
		if !ok || !info.Available {
			if *p.val {
				reason := info.Reason
				if reason == "" {
					reason = fmt.Sprintf("%s does not support this library type.", p.app)
				}
				return core.Presentations{}, core.PresentationOverrides{}, fmt.Errorf("%w: %s", ErrInvalid, reason)
			}
			continue
		}
		nextOv.Set(p.app, p.val)
	}

	var eff core.Presentations
	if adapterOnly {
		eff.AdapterOnly = true
		return eff, nextOv, nil
	}
	for _, info := range resolved.Apps {
		if !info.Available {
			continue
		}
		enabled := info.Enabled
		if resolved.AdapterOnly && !adapterOnly {
			enabled = info.DefaultOn
		}
		if ov := nextOv.Get(info.App); ov != nil {
			enabled = *ov
		}
		if enabled {
			copyAppPresentation(&eff, resolved.Available, info.App)
		}
	}
	return eff, nextOv, nil
}

// OverridesFromLegacyPresent translates a legacy full Presentations payload
// (from older console or API callers that send `present` instead of
// `presentApps`) into sticky owner overrides for every app that fits.
func OverridesFromLegacyPresent(resolved core.ResolvedPresentations, current core.PresentationOverrides, legacy core.Presentations) (core.Presentations, core.PresentationOverrides, error) {
	patch := AppSwitchPatch{
		AdapterOnly: boolPtr(legacy.AdapterOnly),
	}
	if !legacy.AdapterOnly {
		patch.Vela = boolPtr(legacy.Vela != nil)
		patch.Plex = boolPtr(legacy.Plex != nil)
		patch.MediaBrowser = boolPtr(legacy.MediaBrowser != nil)
		patch.Stremio = boolPtr(legacy.Stremio != nil)
		patch.Xtream = boolPtr(legacy.Xtream != nil)
		patch.Silo = boolPtr(legacy.Silo != nil)
	}
	return ApplyAppSwitches(resolved, current, patch)
}

func inferMediaClass(in KindPresentationInput) core.MediaClass {
	// 1. Explicit kind-level media class.
	if s := strings.TrimSpace(in.Media); core.ValidMediaClass(s) {
		return core.MediaClass(s)
	}

	// 2. Explicit level roles.
	for _, lv := range in.Levels {
		switch core.MediaRole(strings.TrimSpace(lv.Role)) {
		case core.MediaRoleArtist, core.MediaRoleAlbum, core.MediaRoleTrack:
			return core.MediaClassMusic
		case core.MediaRoleAudiobook:
			return core.MediaClassAudiobook
		case core.MediaRoleBook:
			return core.MediaClassBook
		}
	}
	for _, lv := range in.Levels {
		if core.MediaRole(strings.TrimSpace(lv.Role)) == core.MediaRolePart {
			return core.MediaClassAudiobook
		}
	}

	// 3. Declared present hints: MediaBrowser first (richer taxonomy), then Plex.
	if in.Present.MediaBrowser != nil {
		if c := mediaClassFromMBCollection(in.Present.MediaBrowser.CollectionType, ""); c != "" {
			return c
		}
		if c := mediaClassFromMBItemTypes(in.Present.MediaBrowser.ItemTypes); c != "" {
			return c
		}
	}
	if in.Present.Plex != nil {
		switch strings.TrimSpace(in.Present.Plex.Base) {
		case "artist":
			return core.MediaClassMusic
		case "photo":
			return core.MediaClassPhoto
		case "movie", "show":
			return core.MediaClassVideo
		}
	}

	// 4. Level key shape.
	for _, lv := range in.Levels {
		switch strings.ToLower(strings.TrimSpace(lv.Key)) {
		case "artist", "artists", "album", "albums", "track", "tracks", "song", "songs":
			return core.MediaClassMusic
		case "audiobook", "audiobooks":
			return core.MediaClassAudiobook
		case "book", "books", "comic", "comics", "manga", "issue", "issues":
			return core.MediaClassBook
		case "podcast", "podcasts":
			return core.MediaClassPodcast
		case "photo", "photos", "photo-album", "photo_album", "photoalbum", "picture", "pictures", "image", "images":
			return core.MediaClassPhoto
		}
	}

	return core.MediaClassVideo
}

func mediaClassFromMBCollection(ct string, fallback core.MediaClass) core.MediaClass {
	switch strings.ToLower(strings.TrimSpace(ct)) {
	case "music":
		return core.MediaClassMusic
	case "audiobooks":
		return core.MediaClassAudiobook
	case "books":
		return core.MediaClassBook
	case "photos":
		return core.MediaClassPhoto
	case "movies", "tvshows", "homevideos":
		return core.MediaClassVideo
	default:
		return fallback
	}
}

func mediaClassFromMBItemTypes(itemTypes map[string]string) core.MediaClass {
	for _, v := range itemTypes {
		switch v {
		case "MusicArtist", "MusicAlbum", "Audio":
			return core.MediaClassMusic
		case "AudioBook":
			return core.MediaClassAudiobook
		case "Book":
			return core.MediaClassBook
		case "Photo", "PhotoAlbum":
			return core.MediaClassPhoto
		case "Movie", "Series", "Season", "Episode":
			return core.MediaClassVideo
		}
	}
	return ""
}

func levelTreeShape(levels []KindPresentationLevel) (depths []int, isLeaf []bool, maxDepth int) {
	n := len(levels)
	depths = make([]int, n)
	isLeaf = make([]bool, n)
	if n == 0 {
		return depths, isLeaf, 0
	}
	idxByKey := make(map[string]int, n)
	for i, lv := range levels {
		idxByKey[lv.Key] = i
		isLeaf[i] = true
	}
	parentIdx := make([]int, n)
	for i, lv := range levels {
		if i == 0 || lv.Members {
			parentIdx[i] = -1
			continue
		}
		if lv.Parent != "" {
			if p, ok := idxByKey[lv.Parent]; ok && p < i {
				parentIdx[i] = p
				isLeaf[p] = false
				continue
			}
		}
		parentIdx[i] = i - 1
		isLeaf[i-1] = false
	}
	for i := range levels {
		d := 0
		for cur := parentIdx[i]; cur >= 0 && d <= n; cur = parentIdx[cur] {
			d++
		}
		depths[i] = d
		if d > maxDepth {
			maxDepth = d
		}
	}
	return depths, isLeaf, maxDepth
}

func effectiveLevelClass(lv KindPresentationLevel, kindClass core.MediaClass) core.MediaClass {
	if core.ValidMediaClass(lv.Media) {
		return core.MediaClass(lv.Media)
	}
	return kindClass
}

func inferLevelRoles(class core.MediaClass, in KindPresentationInput) []core.MediaRole {
	n := len(in.Levels)
	roles := make([]core.MediaRole, n)
	var mbTypes map[string]string
	if in.Present.MediaBrowser != nil {
		mbTypes = in.Present.MediaBrowser.ItemTypes
	}
	depths, isLeaf, maxDepth := levelTreeShape(in.Levels)
	treeHeight := maxDepth + 1

	for i, lv := range in.Levels {
		effClass := effectiveLevelClass(lv, class)
		// 1. Explicit level role (free text, e.g. "concert").
		if r := strings.TrimSpace(lv.Role); r != "" {
			roles[i] = core.MediaRole(r)
			continue
		}
		// 2. Explicit video title level defaults to item role.
		if lv.Playable && lv.Media == string(core.MediaClassVideo) {
			roles[i] = core.MediaRoleItem
			continue
		}
		// 3. Declared MediaBrowser itemTypes hint.
		if mbTypes != nil {
			if r := roleFromMBItemType(mbTypes[lv.Key]); r != "" {
				roles[i] = r
				continue
			}
		}
		// 4. Level key heuristic.
		if r := roleFromLevelKey(lv.Key, effClass); r != "" {
			roles[i] = r
			continue
		}
	}

	// 5. Fill remaining gaps by class + hierarchy depth.
	for i, lv := range in.Levels {
		if roles[i] != "" {
			continue
		}
		effClass := effectiveLevelClass(lv, class)
		roles[i] = defaultRoleForNode(effClass, depths[i], isLeaf[i], treeHeight)
	}
	return roles
}

func roleFromMBItemType(mb string) core.MediaRole {
	switch mb {
	case "MusicArtist":
		return core.MediaRoleArtist
	case "MusicAlbum":
		return core.MediaRoleAlbum
	case "Audio":
		return core.MediaRoleTrack
	case "AudioBook":
		return core.MediaRoleAudiobook
	case "Book":
		return core.MediaRoleBook
	case "PhotoAlbum", "Season":
		return core.MediaRoleCollection
	case "Series":
		return core.MediaRoleShow
	case "Episode":
		return core.MediaRoleEpisode
	case "Movie", "Video", "Photo":
		return core.MediaRoleItem
	default:
		return ""
	}
}

func roleFromLevelKey(key string, class core.MediaClass) core.MediaRole {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "artist", "artists":
		return core.MediaRoleArtist
	case "album", "albums":
		return core.MediaRoleAlbum
	case "track", "tracks", "song", "songs":
		return core.MediaRoleTrack
	case "audiobook", "audiobooks":
		return core.MediaRoleAudiobook
	case "book", "books", "comic", "comics", "issue", "issues":
		return core.MediaRoleBook
	case "part", "parts", "chapter", "chapters":
		if class == core.MediaClassAudiobook || class == core.MediaClassBook {
			return core.MediaRolePart
		}
	case "series":
		if class == core.MediaClassBook || class == core.MediaClassAudiobook {
			return core.MediaRoleSeries
		}
		return core.MediaRoleShow
	case "show", "shows", "podcast", "podcasts":
		return core.MediaRoleShow
	case "episode", "episodes":
		return core.MediaRoleEpisode
	case "season", "seasons", "collection", "collections", "photo-album", "photo_album", "photoalbum":
		return core.MediaRoleCollection
	}
	return ""
}

func defaultRoleForNode(class core.MediaClass, depth int, isLeaf bool, total int) core.MediaRole {
	switch class {
	case core.MediaClassMusic:
		switch {
		case total <= 1:
			return core.MediaRoleAlbum
		case total == 2:
			if depth == 0 {
				return core.MediaRoleAlbum
			}
			return core.MediaRoleTrack
		default:
			if depth == 0 {
				return core.MediaRoleArtist
			}
			if isLeaf {
				return core.MediaRoleTrack
			}
			return core.MediaRoleAlbum
		}
	case core.MediaClassAudiobook:
		if total <= 1 || depth == 0 {
			return core.MediaRoleAudiobook
		}
		return core.MediaRolePart
	case core.MediaClassBook:
		if total <= 1 || isLeaf {
			return core.MediaRoleBook
		}
		return core.MediaRoleSeries
	case core.MediaClassPodcast:
		if total <= 1 || isLeaf {
			return core.MediaRoleEpisode
		}
		return core.MediaRoleShow
	case core.MediaClassPhoto:
		if total <= 1 || isLeaf {
			return core.MediaRoleItem
		}
		return core.MediaRoleCollection
	default: // video
		switch {
		case total <= 1:
			return core.MediaRoleItem
		case total == 2:
			if depth == 0 {
				return core.MediaRoleShow
			}
			return core.MediaRoleEpisode
		default:
			if depth == 0 {
				return core.MediaRoleShow
			}
			if isLeaf {
				return core.MediaRoleEpisode
			}
			return core.MediaRoleCollection
		}
	}
}

func inferVelaKindType(_ core.MediaClass, levels []KindPresentationLevel, present core.Presentations) string {
	// Vela's "shows" projection requires at least two levels.
	if len(levels) >= 2 {
		if present.MediaBrowser != nil && present.MediaBrowser.CollectionType == "movies" {
			return "movies"
		}
		if present.Plex != nil && present.Plex.Base == "movie" {
			return "movies"
		}
		return "shows"
	}
	if len(levels) == 0 {
		if present.MediaBrowser != nil && present.MediaBrowser.CollectionType == "tvshows" {
			return "shows"
		}
		if present.Plex != nil && present.Plex.Base == "show" {
			return "shows"
		}
	}
	return "movies"
}

func inferPlexBase(class core.MediaClass, multiLevel bool) (base, reason string) {
	switch class {
	case core.MediaClassVideo:
		if multiLevel {
			return "show", ""
		}
		return "movie", ""
	case core.MediaClassMusic:
		return "artist", ""
	case core.MediaClassPhoto:
		return "photo", ""
	case core.MediaClassBook:
		return "", reasonPlexBook
	case core.MediaClassAudiobook:
		return "", reasonPlexAudiobook
	case core.MediaClassPodcast:
		return "", reasonPlexPodcast
	default:
		return "movie", ""
	}
}

func inferMediaBrowserCollectionType(class core.MediaClass, multiLevel bool) string {
	switch class {
	case core.MediaClassMusic:
		return "music"
	case core.MediaClassAudiobook:
		return "audiobooks"
	case core.MediaClassBook:
		return "books"
	case core.MediaClassPhoto:
		return "photos"
	case core.MediaClassPodcast:
		return "music"
	default: // video
		if multiLevel {
			return "tvshows"
		}
		return "movies"
	}
}

func inferMediaBrowserItemTypes(class core.MediaClass, levels []KindPresentationLevel, roles []core.MediaRole, existing map[string]string) map[string]string {
	if len(levels) == 0 && len(existing) == 0 {
		return nil
	}
	out := make(map[string]string, len(levels)+len(existing))
	maps.Copy(out, existing)
	depths, isLeaf, maxDepth := levelTreeShape(levels)
	treeHeight := maxDepth + 1
	for i, lv := range levels {
		if lv.Key == "" || out[lv.Key] != "" {
			continue
		}
		var role core.MediaRole
		if i < len(roles) {
			role = roles[i]
		}
		if lv.Playable && lv.Media == string(core.MediaClassVideo) {
			if class != core.MediaClassVideo || depths[i] > 0 {
				out[lv.Key] = "Video"
			} else {
				out[lv.Key] = "Movie"
			}
			continue
		}
		effClass := effectiveLevelClass(lv, class)
		if lv.Members {
			// A list (a playlist of the kind's own items) is a container whose
			// children are its members, never a single playable item.
			out[lv.Key] = listMBItemType(effClass, treeHeight)
			continue
		}
		out[lv.Key] = inferMBItemTypeForLevel(effClass, role, depths[i], isLeaf[i], treeHeight)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// listMBItemType is the MediaBrowser container a list level presents as: the
// class's folder type, which Jellyfin and Emby open by listing its children.
func listMBItemType(class core.MediaClass, treeHeight int) string {
	switch class {
	case core.MediaClassMusic, core.MediaClassPodcast, core.MediaClassAudiobook:
		return "MusicAlbum"
	case core.MediaClassPhoto:
		return "PhotoAlbum"
	case core.MediaClassBook:
		return "Series"
	}
	return inferMBItemTypeForLevel(class, "", 0, false, max(treeHeight, 2))
}

func inferMBItemTypeForLevel(class core.MediaClass, role core.MediaRole, depth int, isLeaf bool, total int) string {
	switch class {
	case core.MediaClassMusic:
		switch role {
		case core.MediaRoleArtist:
			return "MusicArtist"
		case core.MediaRoleAlbum:
			return "MusicAlbum"
		case core.MediaRoleTrack:
			return "Audio"
		}
		if total <= 1 {
			return "MusicAlbum"
		}
		if isLeaf {
			return "Audio"
		}
		if depth == 0 && total >= 3 {
			return "MusicArtist"
		}
		return "MusicAlbum"
	case core.MediaClassAudiobook:
		if total > 1 && !isLeaf {
			return "MusicAlbum"
		}
		if role == core.MediaRolePart {
			return "Audio"
		}
		return "AudioBook"
	case core.MediaClassBook:
		if total > 1 && !isLeaf {
			return "Series"
		}
		return "Book"
	case core.MediaClassPhoto:
		if total > 1 && !isLeaf {
			return "PhotoAlbum"
		}
		return "Photo"
	case core.MediaClassPodcast:
		if total > 1 && !isLeaf {
			if depth == 0 && total >= 3 {
				return "MusicArtist"
			}
			return "MusicAlbum"
		}
		return "Audio"
	default: // video
		switch {
		case total <= 1:
			return "Movie"
		case total == 2:
			if depth == 0 {
				return "Series"
			}
			return "Episode"
		default:
			if depth == 0 {
				return "Series"
			}
			if isLeaf {
				return "Episode"
			}
			return "Season"
		}
	}
}

func copyAppPresentation(dst *core.Presentations, src core.Presentations, app string) {
	switch app {
	case "vela":
		if src.Vela != nil {
			cp := *src.Vela
			cp.Default = nil
			dst.Vela = &cp
		}
	case "plex":
		if src.Plex != nil {
			cp := *src.Plex
			cp.Default = nil
			dst.Plex = &cp
		}
	case "mediaBrowser":
		if src.MediaBrowser != nil {
			cp := core.MediaBrowserPresentation{
				CollectionType: src.MediaBrowser.CollectionType,
			}
			if len(src.MediaBrowser.ItemTypes) > 0 {
				cp.ItemTypes = make(map[string]string, len(src.MediaBrowser.ItemTypes))
				maps.Copy(cp.ItemTypes, src.MediaBrowser.ItemTypes)
			}
			dst.MediaBrowser = &cp
		}
	case "stremio":
		if src.Stremio != nil {
			cp := *src.Stremio
			cp.Default = nil
			dst.Stremio = &cp
		}
	case "xtream":
		if src.Xtream != nil {
			cp := *src.Xtream
			cp.Default = nil
			dst.Xtream = &cp
		}
	case "silo":
		if src.Silo != nil {
			cp := *src.Silo
			cp.Default = nil
			dst.Silo = &cp
		}
	}
}

func defaultFlag(hasExplicit, explicit bool) bool {
	if hasExplicit {
		return explicit
	}
	return true
}

func ptrBoolVal(p *bool) bool {
	if p == nil {
		return true
	}
	return *p
}

func boolPtr(v bool) *bool { return &v }

func velaDefault(p *core.VelaPresentation) *bool {
	if p == nil {
		return nil
	}
	return p.Default
}

func plexDefault(p *core.PlexPresentation) *bool {
	if p == nil {
		return nil
	}
	return p.Default
}

func mbDefault(p *core.MediaBrowserPresentation) *bool {
	if p == nil {
		return nil
	}
	return p.Default
}

func stremioDefault(p *core.StremioPresentation) *bool {
	if p == nil {
		return nil
	}
	return p.Default
}

func xtreamDefault(p *core.XtreamPresentation) *bool {
	if p == nil {
		return nil
	}
	return p.Default
}

func siloDefault(p *core.SiloPresentation) *bool {
	if p == nil {
		return nil
	}
	return p.Default
}
