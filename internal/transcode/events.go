package transcode

import (
	"github.com/braedonsaunders/ether/internal/events"
)

// WithEvents configures the domain event bus for transcode events such as
// hardware-to-software fallback notifications.
func WithEvents(bus *events.Bus) Option {
	return func(o *options) { o.events = bus }
}

func (s *Session) emitTranscodeFallback(fromHWAccel, toEncoder, reason string) {
	if s == nil || s.m == nil || s.m.opts.events == nil {
		return
	}
	if toEncoder == "" {
		toEncoder = "libx264"
	}
	if fromHWAccel == "" {
		fromHWAccel = "hardware"
	}
	active := int(s.m.activeTranscodes.Load())
	if active < 0 {
		active = 0
	}
	_ = s.m.opts.events.PublishTyped(
		events.EventPlaybackTranscodeFallback,
		events.PlaybackTranscodeFallbackPayload{
			SessionID:        s.id,
			FromHWAccel:      fromHWAccel,
			ToEncoder:        toEncoder,
			Reason:           reason,
			ActiveTranscodes: active,
		},
		events.WithCorrelation(events.Correlation{SessionID: s.id}),
	)
}
