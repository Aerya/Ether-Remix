package core

// ClientCapabilityOverride is an owner edit of one registry client.
// Nil slices and zero numbers leave the registry value in place.
// Runtime declarations win unless Force is set for the edited facts.
type ClientCapabilityOverride struct {
	Force   bool   `json:"force,omitempty"`
	ID      string `json:"id,omitempty"`
	Product string `json:"product,omitempty"`
	// ProductExact marks a product-only override already scoped by the app editor
	// or migrated from the old substring matcher.
	ProductExact bool `json:"productExact,omitempty"`
	// ProductMatch preserves legacy substring selectors. New app edits are exact.
	ProductMatch string `json:"productMatch,omitempty"`
	// CopiedFrom identifies a migrated shared selector for the console badge.
	CopiedFrom string `json:"copiedFrom,omitempty"`
	Platform   string `json:"platform,omitempty"`
	Device     string `json:"device,omitempty"`
	// Version is the minimum client version (dotted numbers) this override requires.
	Version             string   `json:"version,omitempty"`
	VideoCodecs         []string `json:"videoCodecs,omitempty"`
	AudioCodecs         []string `json:"audioCodecs,omitempty"`
	Containers          []string `json:"containers,omitempty"`
	SubtitleCodecs      []string `json:"subtitleCodecs,omitempty"`
	DolbyVisionProfiles []int    `json:"dolbyVisionProfiles,omitempty"`
	MaxHeight           int      `json:"maxHeight,omitempty"`
	MaxBitrateKbps      int      `json:"maxBitrateKbps,omitempty"`
	HDR10               *bool    `json:"hdr10,omitempty"`
	DolbyVision         *bool    `json:"dolbyVision,omitempty"`
	// AlwaysEncodeVideo forces every transcoded or remuxed video stream for
	// matching clients onto the encoded path (keyframes on segment boundaries)
	// instead of stream-copying video. Direct play of the original file is
	// unaffected. Applies even when Force is false.
	AlwaysEncodeVideo bool   `json:"alwaysEncodeVideo,omitempty"`
	Notes             string `json:"notes,omitempty"`
}
