package frontend

import "errors"

// ErrForbidden reports an authenticated viewer who may not access or mutate
// the requested resource.
var ErrForbidden = errors.New("frontend: forbidden")

// Screens serves plugin screens and surfaces drawn from documents
// (docs/plugin-screens.md); see PluginScreens.
type Screens = PluginScreens

// ScreensGeneration optionally reports a monotonic generation for screen
// navigation changes so frontend bootstrap caches invalidate promptly.
type ScreensGeneration interface {
	Generation() uint64
}
