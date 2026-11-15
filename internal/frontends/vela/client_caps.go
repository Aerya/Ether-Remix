package vela

import (
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/caps"
)

// Detail and Play build the same declaration with current network and owner rules.
func (i *instance) clientCapabilities(r *http.Request, v *frontend.Viewer, c client) (frontend.CapabilityResult, error) {
	profile := caps.DeviceProfile{MaxStreamingBitrate: c.MaxBitrate, DirectPlayProfiles: []caps.DirectPlayProfile{{Type: "Video", Container: strings.Join(c.Containers, ","), VideoCodec: strings.Join(c.VideoCodecs, ","), AudioCodec: strings.Join(c.AudioCodecs, ",")}}}
	if c.HLS || c.MSE {
		container := "mp4"
		profile.TranscodingProfiles = []caps.TranscodingProfile{{Type: "Video", Protocol: "hls", Container: container, VideoCodec: strings.Join(c.VideoCodecs, ","), AudioCodec: strings.Join(c.AudioCodecs, ",")}}
	}
	for _, format := range c.SubtitleFormats {
		if (c.HLS || c.MSE) && format == "webvtt" {
			profile.SubtitleProfiles = append(profile.SubtitleProfiles, caps.SubtitleProfile{Format: "ether-vtt-window", Method: "External"})
		}
		profile.SubtitleProfiles = append(profile.SubtitleProfiles, caps.SubtitleProfile{Format: format, Method: "External"})
	}
	hdr, dv := false, false
	for _, format := range c.HDR {
		switch format {
		case "hdr10", "hlg":
			hdr = true
		case "dv":
			dv = true
		}
	}
	result, err := i.host.Capabilities.Build(i.identityContext(r), frontend.CapabilityRequest{Viewer: v, Device: caps.DeviceInfo{Protocol: "vela", Product: "Vela", Platform: c.Device.Platform, Version: c.Device.AppVersion, ClientID: c.Device.ID, DeviceName: c.Device.Name, HDR10: hdr, DolbyVision: dv, VideoCodecs: c.VideoCodecs, AudioCodecs: c.AudioCodecs, Containers: c.Containers, SubtitleCodecs: c.SubtitleFormats, MaxHeight: c.MaxHeight, MaxBitrateKbps: c.MaxBitrate / 1000}, Profile: profile, UserAgent: r.UserAgent()})
	// Preserve exact declarations, after owner rules have denied any ranges.
	for _, format := range c.HDR {
		if ((format == "hdr10" || format == "hlg") && result.Client.HDR10) || (format == "dv" && result.Client.DolbyVision) {
			result.Client.HDRFormats = append(result.Client.HDRFormats, format)
		}
	}
	return result, err
}
