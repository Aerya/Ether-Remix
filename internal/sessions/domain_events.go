package sessions

import (
	"context"
	"errors"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
)

func admissionPolicyName(err error, remote bool, lookupDenied bool) string {
	var bedtime *core.ErrBedtime
	switch {
	case errors.As(err, &bedtime):
		return "bedtime"
	case errors.Is(err, core.ErrUnreleased):
		return "unreleased"
	case lookupDenied:
		return "content_policy"
	case remote:
		return "remote_access"
	default:
		return "access_policy"
	}
}

func admissionClientLabel(caps core.ClientCaps, device core.Device, protocol string) string {
	if p := strings.TrimSpace(caps.Product); p != "" {
		return p
	}
	if n := strings.TrimSpace(caps.DeviceName); n != "" {
		return n
	}
	if id := strings.TrimSpace(device.ID); id != "" {
		return id
	}
	return strings.TrimSpace(protocol)
}

func (b *Broker) emitAdmissionDenied(v *core.Viewer, key core.RatingKey, titleName string, caps core.ClientCaps, device core.Device, protocol string, lookupDenied bool, err error) {
	if b == nil || b.deps.DomainEvents == nil || err == nil {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	var viewerID int64
	var username string
	if v != nil {
		viewerID = v.ID
		username = v.Username
	}
	titleKey := ""
	if key != 0 {
		titleKey = key.String()
	}
	reason := strings.TrimSpace(err.Error())
	if reason == "" {
		reason = "playback admission denied"
	}
	_ = b.deps.DomainEvents.PublishTyped(
		events.EventPlaybackAdmissionDenied,
		events.PlaybackAdmissionDeniedPayload{
			TitleKey:  titleKey,
			TitleName: titleName,
			ViewerID:  viewerID,
			Username:  username,
			Client:    admissionClientLabel(caps, device, protocol),
			Remote:    caps.Remote,
			Policy:    admissionPolicyName(err, caps.Remote, lookupDenied),
			Reason:    reason,
		},
		events.WithCorrelation(events.Correlation{
			ViewerID: viewerID,
			TitleID:  titleKey,
			DeviceID: device.ID,
		}),
	)
}
