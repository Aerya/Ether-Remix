package sessions

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/transcode"
)

func capacityError(err error) error {
	if errors.Is(err, transcode.ErrTooManyTranscodes) {
		return &frontend.TranscodeCapacityError{RetryAfter: 5 * time.Second, Cause: err}
	}
	return err
}

func (b *Broker) recordEncoderFailure(playID string, err error) error {
	if msg := frontend.VideoEncoderCapacityMessage(err); msg != "" {
		if recorder, ok := b.deps.Playback.(interface{ NoteFailedStart(string, string) }); ok {
			var capacity *transcode.EncoderCapacityError
			if errors.As(err, &capacity) && len(capacity.Viewers) > 0 {
				msg += " (" + strings.Join(capacity.Viewers, ", ") + ")"
			}
			recorder.NoteFailedStart(playID, msg)
		}
	}
	return capacityError(err)
}

func (b *Broker) withSourceDecision(ctx context.Context, req frontend.PlanRequest) context.Context {
	candidate := copyRequest(req)
	return core.WithSourceDecision(ctx, core.SourceDecision{
		Options: candidate.Intent.Options,
		Decide: func(info core.MediaInfo, res *core.Resolution) core.Decision {
			probe := &core.PlaySession{Info: info, Resolution: res}
			if info.Estimated {
				d, _ := b.decideProfile(probe, candidate)
				return d
			}
			request := candidate
			d, _ := b.admitPlanDecision(probe, &request)
			return d
		},
	})
}
