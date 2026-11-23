package scene

import "strings"

// DefaultWords supplies the binder's default role-label copy when a scene
// references {words.*} tokens directly instead of baking literal headings.
// {words.tagline} is bound from Brand.Tagline per style.
var DefaultWords = map[string]string{
	"words.back":    "NOW RESUMING",
	"words.break":   "BACK IN A MOMENT",
	"words.now":     "NOW PLAYING",
	"words.offair":  "OFF AIR",
	"words.tonight": "TONIGHT",
	"words.upNext":  "UP NEXT",
}

// SupportedSlots is the authoritative sorted list of live data and asset slots
// resolved by both the Go playout binder and the console preview renderer.
var SupportedSlots = []string{
	"brand.logo",
	"channel.name",
	"list[0].meta",
	"list[0].time",
	"list[0].title",
	"list[1].meta",
	"list[1].time",
	"list[1].title",
	"list[2].meta",
	"list[2].time",
	"list[2].title",
	"list[3].meta",
	"list[3].time",
	"list[3].title",
	"next.backdrop",
	"next.logline",
	"next.meta",
	"next.poster",
	"next.reason",
	"next.time",
	"next.title",
	"now.logline",
	"now.meta",
	"now.title",
	"offair.message",
	"viewer.greeting",
	"viewer.name",
}

// SupportedWordTokens lists every {words.*} token resolved by the binder.
var SupportedWordTokens = []string{
	"words.back",
	"words.break",
	"words.now",
	"words.offair",
	"words.tagline",
	"words.tonight",
	"words.upNext",
}

// SupportedBrandTokens lists the shared brand palette, typography, and energy
// tokens resolved by the binder on every non-system style.
var SupportedBrandTokens = []string{
	"accent",
	"bg",
	"brand.logo",
	"energy",
	"glow",
	"text",
	"type.body",
	"type.display",
}

// SupportedTextureTokens lists the per-texture amount tokens populated from Brand.Textures.
var SupportedTextureTokens = []string{
	"textures.chroma",
	"textures.flicker",
	"textures.grain",
	"textures.scanlines",
	"textures.vignette",
}

var supportedStaticKeys = func() map[string]bool {
	m := map[string]bool{
		// Internal/system screen slots preserved for signal/rundown scenes.
		"lineup.heading": true,
		"resume.minutes": true,
		"resume.title":   true,
		"screen.line1":   true,
		"screen.line2":   true,
		"station.id":     true,
		"type.mono":      true,
	}
	for _, list := range [][]string{SupportedSlots, SupportedWordTokens, SupportedBrandTokens, SupportedTextureTokens} {
		for _, k := range list {
			m[k] = true
		}
	}
	return m
}()

// IsSupportedBinding reports whether key (without outer braces, optionally
// carrying an "@alpha" paint suffix) is resolved by the scene binder. When
// controls is non-nil, {control.<id>} must match a declared control ID.
func IsSupportedBinding(key string, controls map[string]bool) bool {
	if at := strings.IndexByte(key, '@'); at >= 0 {
		key = key[:at]
	}
	if supportedStaticKeys[key] {
		return true
	}
	if id, ok := strings.CutPrefix(key, "control."); ok && id != "" {
		if controls == nil {
			return true
		}
		return controls[id]
	}
	return false
}
