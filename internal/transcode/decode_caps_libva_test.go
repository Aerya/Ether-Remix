package transcode

import "testing"

func TestProbeStderrIgnoresLibvaInfo(t *testing.T) {
	banner := "libva info: VA-API version 1.22.0\nlibva info: Trying to open /usr/lib/x86_64-linux-gnu/dri/iHD_drv_video.so\nlibva info: Found init function __vaDriverInit_1_22\nlibva info: va_openDriver() returns 0\n"
	if !probeStderrClean(banner) {
		t.Fatal("libva info banner must not fail a probe")
	}
	if probeStderrClean(banner + "[hevc @ 0x1] Failed to create a decoder\n") {
		t.Fatal("a real FFmpeg error must still fail the probe")
	}
	if !probeStderrClean("") {
		t.Fatal("empty stderr is clean")
	}
}
