package config

// SourceVersionMode is how Play Version treats one source class at one
// resolution.
type SourceVersionMode string

const (
	// SourceVersionOff lists no reserved choice for the class. Empty means
	// the same, so a partly filled matrix reads as "only what I marked".
	SourceVersionOff SourceVersionMode = "off"
	// SourceVersionOffer reserves a choice while the menu has room.
	SourceVersionOffer SourceVersionMode = "offer"
	// SourceVersionRequired reserves a choice ahead of every offered or
	// alternative choice, so the menu limit never drops it.
	SourceVersionRequired SourceVersionMode = "required"
)

// Valid reports a mode the resolver understands.
func (m SourceVersionMode) Valid() bool {
	switch m {
	case "", SourceVersionOff, SourceVersionOffer, SourceVersionRequired:
		return true
	}
	return false
}

// SourceVersionRow is one resolution's source classes.
type SourceVersionRow struct {
	Remux  SourceVersionMode `json:"remux,omitempty"`
	BluRay SourceVersionMode `json:"bluray,omitempty"`
	WEB    SourceVersionMode `json:"web,omitempty"`
}

// SourceVersions is the per-resolution, per-source Play Version matrix
// (Settings › Releases › Versions in the app). Auto never reads it.
type SourceVersions struct {
	UHD SourceVersionRow `json:"2160"`
	FHD SourceVersionRow `json:"1080"`
	HD  SourceVersionRow `json:"720"`
}

// SourceVersionsAll returns the matrix with every cell set to mode.
func SourceVersionsAll(mode SourceVersionMode) SourceVersions {
	row := SourceVersionRow{Remux: mode, BluRay: mode, WEB: mode}
	return SourceVersions{UHD: row, FHD: row, HD: row}
}

// Mode returns the cell for a resolution tier and resolver source class
// ("remux", "disc" or "web"). The 720p row also governs the lower fallback
// tier a sub-720p profile ceiling produces.
func (s SourceVersions) Mode(height int, class string) SourceVersionMode {
	row := s.HD
	switch {
	case height >= 2160:
		row = s.UHD
	case height >= 1080:
		row = s.FHD
	}
	var m SourceVersionMode
	switch class {
	case "remux":
		m = row.Remux
	case "disc":
		m = row.BluRay
	case "web":
		m = row.WEB
	}
	if m == "" {
		return SourceVersionOff
	}
	return m
}

// Invalid returns the JSON path of the first unknown mode, or "".
func (s SourceVersions) Invalid() string {
	for _, r := range []struct {
		key string
		row SourceVersionRow
	}{{"2160", s.UHD}, {"1080", s.FHD}, {"720", s.HD}} {
		for _, c := range []struct {
			key  string
			mode SourceVersionMode
		}{{"remux", r.row.Remux}, {"bluray", r.row.BluRay}, {"web", r.row.WEB}} {
			if !c.mode.Valid() {
				return r.key + "." + c.key
			}
		}
	}
	return ""
}

// SourceVersionPlan is the effective per-source matrix and whether any
// per-source reservation applies. An explicit SourceVersions wins (even all
// off); otherwise the earlier AlternativesPerSource switch means "offer every
// source at every resolution".
func (r Resolver) SourceVersionPlan() (SourceVersions, bool) {
	if r.SourceVersions != nil {
		plan := *r.SourceVersions
		for _, h := range []int{2160, 1080, 720} {
			for _, c := range []string{"remux", "disc", "web"} {
				if plan.Mode(h, c) != SourceVersionOff {
					return plan, true
				}
			}
		}
		return plan, false
	}
	if r.AlternativesPerSource {
		return SourceVersionsAll(SourceVersionOffer), true
	}
	return SourceVersions{}, false
}
