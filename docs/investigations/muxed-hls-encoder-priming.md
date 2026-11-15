# Muxed HLS encoder priming

AAC emits its first packet 1024 samples before the first input sample. At title
zero, the HLS parent muxer's `-avoid_negative_ts disabled` allowed the inner MP4
muxer to write that negative decode time as an unsigned version-1 `tfdt`:
18446744073709550592 at 48 kHz. The earlier audio-only fix normalized this case
only for standalone AAC, leaving muxed video/audio vulnerable.

The shared HLS builder now supplies the inner MP4 muxer with
`avoid_negative_ts=make_non_negative` whenever the selected audio is encoded,
including synthetic silence. This policy handles encoder delay independently
of codec, shifts both tracks together when necessary, and leaves positive seek
timestamps alone. Existing video edit lists and retained-init correction remain
in use. Audio-only AAC keeps its existing `use_editlist=0` policy.

The child MPEG-TS muxer receives the same policy for encoded audio, including
ordinary FFmpeg takeover of an RTX timeline with zero mux delay. Copied audio's
existing transport-clock policy is retained. Normal TS keeps its 1.4-second mux
delay. Plex, Jellyfin and Vela all reach the same FFmpeg builder.

## Verification

`TestHLSMuxedEncoderPriming` runs installed FFmpeg/ffprobe (9.0.2 on this Mac),
generates six-second H.264/AC3 fixtures with zero and two video B-frames, and
runs 30 real muxes: starts 0/2/4 seconds for each of copied-video AAC,
encoded-video AAC, copied-video Opus, TS AAC, and zero-delay RTX-clock TS AAC.
It parses every MP4 fragment's raw unsigned `tfdt`, then probes the joined
output's packet clocks. Restart fragments and init segments go through the
existing retained-init clock correction before checking them.

All fragments have bounded nonnegative raw decode times; all probed packet
PTS/DTS remain nonnegative and below ten seconds (so TS wrap is caught too).
At title zero, audio/video starts differ by at most one output audio frame.
At restarts, copied video may retain an earlier keyframe and encoded audio
retains the existing reorder preroll. The test checks these planned absolute
starts and requires audio/video ends within an audio frame plus a video frame,
providing a short-fixture drift check rather than mistaking preroll for drift.

Representative first packet PTS in seconds:

| Output | Requested start | Audio | Video |
| --- | ---: | ---: | ---: |
| Copy H.264 without B-frames + AAC | 0 | 0 | 0.021375 |
| Same | 2 | 1.994667 | 2 |
| Same | 4 | 4.010667 | 4 |
| Copy H.264 with two B-frames + AAC | 0 | 0.061667 | 0.083 |
| Encode H.264 + AAC | 0 | 0.062 | 0.083333 |
| Copy H.264 without B-frames + Opus | 0 | 0 | 0.0065 |
| Zero-delay TS, copy H.264 + AAC | 0 | 0 | 0.021333 |

The focused test passes (5.12 seconds); `CGO_ENABLED=0 go build -p 2 ./...`
passes, and touched-package `go vet -p 2 ./internal/transcode` passes.
An initial test assertion incorrectly equated restart preroll with
A/V misalignment; the coordinator authorized one rerun after correcting it.

Real-device follow-up: verify Vela strict MSE ingestion, audible lip sync,
start/resume, forward/back seek, continuous regeneration, track changes and
end-of-title on the original AC3 5.1 downmix title. Also verify Plex/Jellyfin
fMP4 and TS start/seek, and RTX fallback timestamp continuity. This fixture
proves muxer clocks, not device playback or long-title acoustic sync.
