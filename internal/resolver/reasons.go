package resolver

// Rejection reasons for the resolution ceiling and floor and pre-probe file
// gates, shown in decision traces and the explain output.
const (
	ReasonAboveProfileMaxResolution = "above profile max resolution"
	ReasonAboveClientMaxResolution  = "above client max resolution"
	ReasonBelowProfileMinResolution = "below profile min resolution"
	ReasonNotAVideoFile             = "not a video file"
	ReasonNeedsVideoTranscode       = "needs a video transcode, and transcoding is off"
)
