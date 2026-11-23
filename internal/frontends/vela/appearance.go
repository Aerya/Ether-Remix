package vela

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var skinIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Built-in documents use the document renderer; older client releases may
// still include these compiled engines.
func skinEngine(id string, installedKind string) string {
	if installedKind == "pack" {
		return "cinematic"
	}
	switch id {
	case "cinematic", "index", "jellytwin", "marquee", "mochi":
		return id
	}
	return "doc"
}

type appearanceEntry struct {
	revision      uint64
	stateKey      string
	skinID        string
	force         bool
	skinsGen      uint64
	appName       string
	appIconDigest string
	appIconExt    string
	branding      object
	packs         []json.RawMessage
	documents     []SkinDocumentEntry
	etag          string
	payload       []byte
}

// Only immutable presentation data is cached. Config reads are snapshots and
// constant time; no catalogue, filesystem, provider or viewer work happens here.
func (i *instance) appearanceDocument() *appearanceEntry {
	skin, force, revision := "cinematic", false, uint64(0)
	var settings map[string]any
	if i.host.HostConfig != nil {
		cfg := i.host.HostConfig().Frontends["vela"]
		revision = cfg.AppearanceRevision
		settings = cfg.Settings
	} else if i.host.Config != nil {
		settings = i.host.Config()
	}
	if id, ok := settings["defaultSkinId"].(string); ok && skinIDPattern.MatchString(id) {
		skin = id
	}
	if f, ok := settings["force"].(bool); ok {
		force = f
	}
	ctx := i.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	skinsGen, packs, documents, kinds := i.skins.Snapshot(ctx)
	if i.skins != nil && !IsBuiltinSkinID(skin) && kinds[skin] == "" {
		skin = "cinematic"
	}
	stateKey := canonicalAppearanceState(settings)
	i.appearanceMu.Lock()
	defer i.appearanceMu.Unlock()
	if c := i.appearanceCache; c != nil && ((c.revision > revision && c.skinsGen >= skinsGen) || (c.revision == revision && c.stateKey == stateKey && c.skinID == skin && c.force == force && c.skinsGen == skinsGen)) {
		return c
	}
	branding, appName := brandingFromSettings(settings)
	iconDigest, iconExt := "", ""
	if icon, ok := parseAppIconSetting(settings["appIcon"]); ok {
		iconDigest = icon.Digest
		iconExt = icon.Ext
	}
	// The client merges its built-ins from /vela/skins/catalog.json itself.
	payload, _ := json.Marshal(object{
		"revision":        revision,
		"defaultSkinId":   skin,
		"defaultEngineId": skinEngine(skin, kinds[skin]),
		"branding":        branding,
		"force":           force,
		"packs":           packs,
		"documents":       documents,
	})
	c := &appearanceEntry{
		revision:      revision,
		stateKey:      stateKey,
		skinID:        skin,
		force:         force,
		skinsGen:      skinsGen,
		appName:       appName,
		appIconDigest: iconDigest,
		appIconExt:    iconExt,
		branding:      branding,
		packs:         packs,
		documents:     documents,
		payload:       payload,
		etag:          `"` + hash(string(payload)) + `"`,
	}
	i.appearanceCache = c
	return c
}

// Public so sign-in can use the owner's look. Never includes viewer information.
func (i *instance) appearance(w http.ResponseWriter, r *http.Request) {
	c := i.appearanceDocument()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	w.Header().Set("ETag", c.etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	for _, tag := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == c.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	if r.Method != http.MethodHead {
		_, _ = w.Write(c.payload)
	}
}

// A viewer subscribes only while using Vela. No timers or goroutines exist at
// idle. Reconnection always starts with the latest snapshot, including revision 0.
func (i *instance) appearanceEvents(w http.ResponseWriter, r *http.Request) {
	if _, _, err := i.viewer(r); err != nil {
		fail(w, err, r)
		return
	}
	flush, ok := w.(http.Flusher)
	if !ok {
		i.unavailable(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var previous string
	count := 0
	for {
		c := i.appearanceDocument()
		if c.etag != previous {
			cursor := strconv.FormatUint(c.revision, 10)
			b, _ := json.Marshal(object{"type": "appearance", "cursor": cursor, "appearance": json.RawMessage(c.payload)})
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := fmt.Fprintf(w, "id: %s\nevent: appearance\ndata: %s\n\n", cursor, b); err != nil {
				return
			}
			flush.Flush()
			previous = c.etag
		} else if count%15 == 0 {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flush.Flush()
		}
		count++
		select {
		case <-r.Context().Done():
			return
		case <-i.ctx.Done():
			return
		case <-ticker.C:
			// Revoked grants must not keep an authenticated stream alive.
			if count%15 == 0 {
				if _, _, err := i.viewer(r); err != nil {
					return
				}
			}
		}
	}
}
