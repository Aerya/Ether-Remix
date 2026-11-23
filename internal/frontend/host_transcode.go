package frontend

import (
	"context"
	"io"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

const (
	ContainerMPEGTS   = transcode.ContainerMPEGTS
	ContainerFMP4     = transcode.ContainerFMP4
	ContainerMatroska = transcode.ContainerMatroska

	InitSegment        = transcode.InitSegment
	InitSegmentName    = transcode.InitSegmentName
	MatroskaHeaderName = transcode.MatroskaHeaderName
)

var ErrTooManyTranscodes = transcode.ErrTooManyTranscodes

type TranscodePolicy = transcode.Policy
type ClientPolicy = transcode.ClientPolicy
type PlaybackDefaults = transcode.PlaybackDefaults
type BIF = transcode.BIF
type BIFFrame = transcode.BIFFrame

func ParseBIF(data []byte) (*BIF, error) { return transcode.ParseBIF(data) }

func MarshalBIF(frames []BIFFrame) ([]byte, error) { return transcode.MarshalBIF(frames) }

func ParseSegmentName(name string) (int, bool) { return transcode.ParseSegmentName(name) }

func IsImageSubtitle(codec string) bool { return transcode.IsImageSubtitle(codec) }

func IsTextSubtitle(codec string) bool { return transcode.IsTextSubtitle(codec) }

func WithSubtitleSource(ctx context.Context, source string) context.Context {
	return transcode.WithSubtitleSource(ctx, source)
}

// WithSubtitleReader hands a subtitle extraction the opened source, so an
// indexed text track is read line by line instead of from the whole file.
func WithSubtitleReader(ctx context.Context, open func() (io.ReaderAt, int64, func(), bool)) context.Context {
	return transcode.WithSubtitleReader(ctx, open)
}

func HasQuirk(product, platform, device, model, version, name string) bool {
	return transcode.HasQuirk(product, platform, device, model, version, name)
}

func PolicyStamp(product, platform, device, deviceName, model, version string, overrides []core.ClientCapabilityOverride) (dv []int, subs []string, dvChanged, subsChanged bool) {
	return transcode.PolicyStamp(product, platform, device, deviceName, model, version, overrides)
}

func PlaybackDefaultsFor(product, platform, device, deviceName, model, version string, overrides []core.ClientCapabilityOverride) PlaybackDefaults {
	return transcode.PlaybackDefaultsFor(product, platform, device, deviceName, model, version, overrides)
}

func ApplyForcedOverrides(c ClientCaps, model string, overrides []core.ClientCapabilityOverride) ClientCaps {
	return transcode.ApplyForcedOverrides(c, model, overrides)
}

func ClientPolicyFor(c ClientCaps) ClientPolicy { return transcode.ClientPolicyFor(c) }

func SupportsDVProfile(c ClientCaps, profile int) bool {
	return transcode.SupportsDVProfile(c, profile)
}

func HasHDR10Base(v *StreamInfo, caps ClientCaps) bool {
	return transcode.HasHDR10Base(v, caps)
}

func BurnsSubtitle(sub StreamInfo, caps ClientCaps, opts DecideOptions) bool {
	return transcode.BurnsSubtitle(sub, caps, opts)
}

func DecideTranscode(info MediaInfo, caps ClientCaps, opts DecideOptions, pol TranscodePolicy) Decision {
	return transcode.Decide(info, caps, opts, pol)
}

func HLSContainer(caps ClientCaps, videoCodec, audioCodec string) string {
	return transcode.HLSContainer(caps, videoCodec, audioCodec)
}

func SegmentedVideoCopyOK(caps ClientCaps, codec string) bool {
	return transcode.SegmentedVideoCopyOK(caps, codec)
}

func SegmentedAudioCopyOK(caps ClientCaps, codec string) bool {
	return transcode.SegmentedAudioCopyOK(caps, codec)
}

func HLSCopyPeakKbps(info MediaInfo, segDur time.Duration) int {
	return transcode.HLSCopyPeakKbps(info, segDur)
}

func HLSVariantAttributes(info MediaInfo, d Decision) string {
	return transcode.HLSVariantAttributes(info, d)
}

// TranscodeOutputSize is the encoded (square-pixel) picture size a
// decision produces from v; see transcode.DecisionOutputSize.
func TranscodeOutputSize(v *StreamInfo, d Decision) (int, int) {
	return transcode.DecisionOutputSize(v, d)
}
