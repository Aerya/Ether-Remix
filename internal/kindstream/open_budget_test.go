package kindstream

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type stubMediaSource struct {
	*bytes.Reader
	id   string
	name string
}

func (s *stubMediaSource) Name() string      { return s.name }
func (s *stubMediaSource) ReleaseID() string { return s.id }
func (s *stubMediaSource) Close() error      { return nil }

func TestOpenSourcesHonorsCandidateOpenTimeoutAndFailsOver(t *testing.T) {
	t.Parallel()

	var failedIDs []string
	var failedErrs []error
	sources := []core.PlayableSource{
		{
			ID:      "hanging-release",
			Pattern: PatternRelease,
			MIME:    "audio/flac",
			Release: &core.ReleaseHint{NZBURL: "https://indexer.example/nzb/hang"},
		},
		{
			ID:      "healthy-release",
			Pattern: PatternRelease,
			MIME:    "audio/flac",
			Release: &core.ReleaseHint{NZBURL: "https://indexer.example/nzb/ok"},
		},
	}

	payload := append([]byte("fLaC"), make([]byte, 64)...)
	bs, win, err := OpenSources(context.Background(), sources, Deps{
		CandidateOpenTimeout: 40 * time.Millisecond,
		OpenReleaseSource: func(ctx context.Context, src core.PlayableSource) (core.MediaSource, error) {
			if src.ID == "hanging-release" {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return &stubMediaSource{Reader: bytes.NewReader(payload), id: src.ID, name: "01.flac"}, nil
		},
		OnSourceFailed: func(_, _ int, src core.PlayableSource, _ time.Duration, err error) {
			failedIDs = append(failedIDs, src.ID)
			failedErrs = append(failedErrs, err)
		},
	})
	if err != nil {
		t.Fatalf("OpenSources failed: %v", err)
	}
	defer bs.Close()

	if win.ID != "healthy-release" {
		t.Fatalf("winning source = %q, want healthy-release", win.ID)
	}
	if len(failedIDs) != 1 || failedIDs[0] != "hanging-release" || !errors.Is(failedErrs[0], context.DeadlineExceeded) {
		t.Fatalf("OnSourceFailed recorded ids=%v errs=%v, want hanging-release + DeadlineExceeded", failedIDs, failedErrs)
	}
}
