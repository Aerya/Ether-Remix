package kindstream

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func releaseDeps(data []byte) Deps {
	return Deps{
		OpenRelease: func(_ context.Context, _ core.ReleaseHint) (core.MediaSource, error) {
			return &fakeMediaSource{id: "rel-digest", name: "game.rom", data: data}, nil
		},
	}
}

func releaseSource(mut func(*core.PlayableSource)) core.PlayableSource {
	src := core.PlayableSource{
		ID:      "rel",
		Pattern: PatternRelease,
		MIME:    "application/octet-stream",
		Release: &core.ReleaseHint{InfoHash: "abc"},
	}
	if mut != nil {
		mut(&src)
	}
	return src
}

// A release whose bytes contradict the plugin's declared SHA-1/CRC32/size
// must fail to open as a plugin-answer error, so the next candidate is
// tried and no fallback is saved over it (#642, #641).
func TestReleaseSourceDigestMismatchFailsOpen(t *testing.T) {
	payload := []byte("the wrong regional rom bytes")
	goodSHA1 := sha1.Sum(payload)
	cases := []struct {
		name string
		mut  func(*core.PlayableSource)
	}{
		{"sha1", func(s *core.PlayableSource) { s.SHA1 = "0000000000000000000000000000000000000000" }},
		{"crc32", func(s *core.PlayableSource) { s.CRC32 = "00000000" }},
		{"sha256", func(s *core.PlayableSource) {
			sum := sha256.Sum256([]byte("other"))
			s.SHA256 = hex.EncodeToString(sum[:])
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Open(context.Background(), releaseSource(tc.mut), releaseDeps(payload))
			if !errors.Is(err, ErrDigestMismatch) || !errors.Is(err, ErrPluginAnswer) {
				t.Fatalf("err = %v, want ErrDigestMismatch wrapping ErrPluginAnswer", err)
			}
		})
	}
	// Matching digests open and serve the bytes.
	src := releaseSource(func(s *core.PlayableSource) {
		s.SHA1 = hex.EncodeToString(goodSHA1[:])
		s.CRC32 = fmt.Sprintf("%08x", crc32.ChecksumIEEE(payload))
		s.Size = int64(len(payload))
	})
	bs, err := Open(context.Background(), src, releaseDeps(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close()
	got, err := io.ReadAll(io.NewSectionReader(bs, 0, bs.Size()))
	if err != nil || string(got) != string(payload) {
		t.Fatalf("served %q err=%v", got, err)
	}
}

// A source larger than maxBytes is hashed as it streams: the read that
// completes the file fails on a mismatch, and the source only ever
// reports verified after the whole file matched.
func TestReleaseSourceStreamingDigest(t *testing.T) {
	payload := []byte("0123456789abcdef0123456789")
	sum := sha256.Sum256(payload)
	src := releaseSource(func(s *core.PlayableSource) {
		s.SHA256 = hex.EncodeToString(sum[:])
		s.MaxBytes = 4 // force the streaming path
	})
	bs, err := Open(context.Background(), src, releaseDeps(payload))
	if err != nil {
		t.Fatal(err)
	}
	dr, ok := bs.(DigestReporter)
	if !ok {
		t.Fatalf("streaming source is %T, want DigestReporter", bs)
	}
	if declared, verified := dr.DigestState(); !declared || verified {
		t.Fatalf("DigestState before reads = %v,%v", declared, verified)
	}
	if _, err := io.ReadAll(io.NewSectionReader(bs, 0, bs.Size())); err != nil {
		t.Fatal(err)
	}
	if _, verified := dr.DigestState(); !verified {
		t.Fatal("source not verified after full sequential read")
	}
	bs.Close()

	bad := releaseSource(func(s *core.PlayableSource) {
		other := sha256.Sum256([]byte("nope"))
		s.SHA256 = hex.EncodeToString(other[:])
		s.MaxBytes = 4
	})
	bs, err = Open(context.Background(), bad, releaseDeps(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close()
	if _, err := io.ReadAll(io.NewSectionReader(bs, 0, bs.Size())); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("streaming mismatch err = %v, want ErrDigestMismatch", err)
	}
}

// When an earlier candidate fails and a later one opens, the failure is
// reported through OnSourceSkipped instead of being dropped (#641).
func TestOpenSourcesReportsSkippedFailures(t *testing.T) {
	payload := []byte("fallback bytes")
	var skipped []SourceFailure
	var winner core.PlayableSource
	deps := releaseDeps(payload)
	deps.OnSourceSkipped = func(failures []SourceFailure, winning core.PlayableSource) {
		skipped = append(skipped, failures...)
		winner = winning
	}
	bad := releaseSource(func(s *core.PlayableSource) {
		s.ID = "first"
		s.CRC32 = "00000000"
	})
	good := releaseSource(func(s *core.PlayableSource) { s.ID = "second" })
	bs, winning, err := OpenSources(context.Background(), []core.PlayableSource{bad, good}, deps)
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close()
	if winning.ID != "second" || winner.ID != "second" {
		t.Fatalf("winning = %q reported %q", winning.ID, winner.ID)
	}
	if len(skipped) != 1 || skipped[0].Source.ID != "first" || !skipped[0].PluginAnswer() {
		t.Fatalf("skipped = %+v, want one plugin-answer failure for first", skipped)
	}
}

// Approximate sizes (for example from a Usenet release_files segment estimate)
// act as a tolerance hint rather than an exact byte check, while exact sizes
// remain strict and grossly wrong approximate sizes are still rejected.
func TestApproximateSizeHintVsStrictSize(t *testing.T) {
	payload := make([]byte, 1_000_000)
	for i := range payload {
		payload[i] = byte(i)
	}
	// ~3.1% larger yEnc-encoded sum / segment estimate within tolerance.
	yencHint := int64(len(payload)) * 1031 / 1000

	// A release source's exact-looking size is the indexer's listing, so
	// it never rejects the file.
	strict := releaseSource(func(s *core.PlayableSource) {
		s.Size = yencHint
	})
	if _, err := Open(context.Background(), strict, releaseDeps(payload)); err != nil {
		t.Fatalf("release size listing rejected the file: %v", err)
	}

	// Explicit SizeApproximate=true: accepts 3% difference.
	approx := releaseSource(func(s *core.PlayableSource) {
		s.Size = yencHint
		s.SizeApproximate = true
	})
	bs, err := Open(context.Background(), approx, releaseDeps(payload))
	if err != nil {
		t.Fatalf("approximate size within tolerance failed: %v", err)
	}
	if bs.Size() != int64(len(payload)) {
		t.Fatalf("opened size = %d, want actual %d", bs.Size(), len(payload))
	}
	_ = bs.Close()

	// Approximate size inherited from Release.Candidate.Files when a plugin
	// passes back the release_files candidate without setting source.SizeApproximate.
	idx0 := 0
	fromCand := releaseSource(func(s *core.PlayableSource) {
		s.Size = yencHint
		s.Release = &core.ReleaseHint{
			FileIndex: &idx0,
			Candidate: &core.SourceCandidate{
				NZBURL: "https://idx.test/album.nzb",
				Files:  []core.ReleaseFile{{Index: 0, Path: "01.flac", Size: yencHint, SizeApproximate: true}},
			},
		}
	})
	bs, err = Open(context.Background(), fromCand, releaseDeps(payload))
	if err != nil {
		t.Fatalf("candidate approximate size failed: %v", err)
	}
	_ = bs.Close()

	// Grossly wrong approximate size (50% off) is still rejected as a plugin answer error.
	wayOff := releaseSource(func(s *core.PlayableSource) {
		s.Size = int64(len(payload)) / 2
		s.SizeApproximate = true
	})
	if _, err := Open(context.Background(), wayOff, releaseDeps(payload)); !errors.Is(err, ErrDigestMismatch) || !errors.Is(err, ErrPluginAnswer) {
		t.Fatalf("grossly wrong approximate size err = %v, want ErrDigestMismatch wrapping ErrPluginAnswer", err)
	}
}

// A release source that names an archive member (e.g. a Usenet release whose
// planner rejects a missing Container.Member with core.ErrWrongContent) must
// classify the mismatch as ErrPluginAnswer just like explicit release.file (#641).
func TestOpenReleaseExplicitContainerMemberMismatchWrapsPluginAnswer(t *testing.T) {
	deps := Deps{
		OpenReleaseSource: func(context.Context, core.PlayableSource) (core.MediaSource, error) {
			return nil, core.ErrWrongContent
		},
	}
	src := core.PlayableSource{
		ID:        "usenet-member",
		Pattern:   PatternRelease,
		Release:   &core.ReleaseHint{NZBURL: "https://idx.test/pack.nzb"},
		Container: &core.ContainerRef{Format: "zip", Member: "Missing.rom"},
	}
	if _, err := Open(context.Background(), src, deps); !errors.Is(err, ErrPluginAnswer) || !errors.Is(err, core.ErrWrongContent) {
		t.Fatalf("err = %v, want ErrPluginAnswer wrapping ErrWrongContent", err)
	}
}
