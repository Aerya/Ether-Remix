package transcode

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

type headerBudgetReader struct {
	r          *bytes.Reader
	bytesRead  atomic.Int64
	maxOffset  atomic.Int64
	readCalled atomic.Int64
}

func (r *headerBudgetReader) ReadMetadataAtContext(_ context.Context, p []byte, off int64) (int, error) {
	r.readCalled.Add(1)
	n, err := r.r.ReadAt(p, off)
	r.bytesRead.Add(int64(n))
	end := off + int64(n)
	for {
		cur := r.maxOffset.Load()
		if end <= cur || r.maxOffset.CompareAndSwap(cur, end) {
			break
		}
	}
	return n, err
}

func (r *headerBudgetReader) ReadAt(p []byte, off int64) (int, error) {
	return r.ReadMetadataAtContext(context.Background(), p, off)
}

func TestProbeHeaderReadsContainerHeaderOnlyWithinBudget(t *testing.T) {
	avcc := fixturePrivate(t, "h264_aac_srt_chapters.mkv", "V_MPEG4/ISO/AVC")
	tracks := el(mkvTracks,
		el(mkvTrackEntry,
			elU(mkvTrackNumber, 1), elU(mkvTrackType, 1), elS(mkvCodecID, "V_MPEG4/ISO/AVC"),
			el(mkvCodecPrivate, avcc), elU(mkvDefaultDur, 41708333),
			el(mkvVideo, elU(mkvPixelWidth, 1920), elU(mkvPixelHeight, 1080))),
		el(mkvTrackEntry,
			elU(mkvTrackNumber, 2), elU(mkvTrackType, 2), elS(mkvCodecID, "A_EAC3"),
			elU(mkvFlagDefault, 1), elS(mkvLanguage, "jpn"),
			el(mkvAudio, elF(mkvSampling, 48000), elU(mkvChannels, 6))),
		el(mkvTrackEntry,
			elU(mkvTrackNumber, 3), elU(mkvTrackType, 2), elS(mkvCodecID, "A_EAC3"),
			elU(mkvFlagDefault, 0), elS(mkvLanguage, "eng"),
			el(mkvAudio, elF(mkvSampling, 48000), elU(mkvChannels, 6))),
	)
	info := el(mkvInfo, elU(mkvTimecodeScl, 1000000), elF(mkvDuration, 5_400_000))
	// Large cluster payload well past the header budget to confirm ProbeHeader
	// never walks media clusters or tail tags.
	cluster := el(mkvCluster, make([]byte, 3<<20))
	body := bytes.Join([][]byte{info, tracks, cluster}, nil)
	file := append(el(mkvEBML, elS(mkvDocType, "matroska")), elUnknown(mkvSegment, body)...)

	src := &headerBudgetReader{r: bytes.NewReader(file)}
	budget := 512 << 10
	got, err := ProbeHeader(context.Background(), src, int64(len(file)), budget)
	if err != nil {
		t.Fatalf("ProbeHeader: %v", err)
	}
	if got.Video() == nil || got.Video().Height != 1080 {
		t.Fatalf("video = %+v, want 1080p", got.Video())
	}
	var langs []string
	for _, st := range got.Streams {
		if st.Type == "audio" {
			langs = append(langs, st.Language)
		}
	}
	if len(langs) != 2 || langs[0] != "jpn" || langs[1] != "eng" {
		t.Fatalf("audio languages = %v, want [jpn eng]", langs)
	}
	if read := src.bytesRead.Load(); read > int64(budget) {
		t.Fatalf("read %d bytes, exceeds budget %d", read, budget)
	}
	if maxOff := src.maxOffset.Load(); maxOff > int64(budget) {
		t.Fatalf("read up to offset %d, expected header-only read <= %d", maxOff, budget)
	}

	// A budget too small to hold the Tracks element fails cleanly with ErrProbeIncomplete.
	tiny := &headerBudgetReader{r: bytes.NewReader(file)}
	if _, err := ProbeHeader(context.Background(), tiny, int64(len(file)), 64); !errors.Is(err, ErrProbeIncomplete) {
		t.Fatalf("tiny budget err = %v, want ErrProbeIncomplete", err)
	}
}
