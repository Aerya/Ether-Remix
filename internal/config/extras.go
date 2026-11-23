package config

import "time"

// Extras controls trailers and other clips attached to titles (Plex
// "Extras" and Cinema Trailers, Jellyfin/Emby, Vela, Silo, Stremio and
// Xtream). On by default whenever a playable source is available (Plex
// Discover via a linked Plex account, or a yt-dlp-compatible resolver
// command). With no working source Ether advertises no extras at all.
type Extras struct {
	// ThemeMusic defaults on when extras are enabled.
	ThemeMusic *bool `json:"themeMusic,omitempty"`
	// TrailerCacheMaxBytes bounds on-demand trailer bytes, including staging.
	// Zero uses 512 MiB; negative disables caching. No prewarming or sweeps.
	TrailerCacheMaxBytes int64 `json:"trailerCacheMaxBytes,omitempty"`
	// Enabled turns extras (and Cinema Trailers) on. Defaults to true.
	Enabled bool `json:"enabled"`
	// DefaultOnMigrated records that the v0.3.19 one-time switch-on
	// migration has run for this config, so a later deliberate off stays off.
	DefaultOnMigrated bool `json:"defaultOnMigrated,omitempty"`
	// ResolverCommand is the path (or PATH name) of a yt-dlp-compatible
	// tool that prints a direct media URL for a video page. Empty disables
	// the command source. Ether ships no resolver binary.
	ResolverCommand string `json:"resolverCommand,omitempty"`
	// ResolverArgs is the fixed argument template passed to
	// ResolverCommand. The placeholders {url}, {site}, {key} and {id} are
	// substituted; the command always runs without a shell. Empty uses a
	// yt-dlp-compatible default ("-g -f b[ext=mp4]/b --no-playlist {url}").
	ResolverArgs []string `json:"resolverArgs,omitempty"`
	// ResolverTimeout bounds one resolver invocation (default 20s).
	ResolverTimeout Duration `json:"resolverTimeout,omitempty"`
	// PrewarmBytes is how much of the feature to pre-buffer while a
	// trailer plays (clamped to 32–64 MiB, default 48 MiB).
	PrewarmBytes int64 `json:"prewarmBytes,omitempty"`
}

// ResolverTimeoutOr returns the configured resolver timeout or dflt.
func (e Extras) ResolverTimeoutOr(dflt time.Duration) time.Duration {
	if e.ResolverTimeout.D() > 0 {
		return e.ResolverTimeout.D()
	}
	return dflt
}

// PrewarmBytesOr returns the trailer pre-buffer size, clamped to 32–64 MiB.
func (e Extras) PrewarmBytesOr() int64 {
	const (
		lo  = 32 << 20
		hi  = 64 << 20
		def = 48 << 20
	)
	if e.PrewarmBytes <= 0 {
		return def
	}
	if e.PrewarmBytes < lo {
		return lo
	}
	if e.PrewarmBytes > hi {
		return hi
	}
	return e.PrewarmBytes
}
