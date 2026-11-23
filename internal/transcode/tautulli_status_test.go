package transcode

import (
	"runtime"
	"testing"
)

func TestPlexHWAccelNames(t *testing.T) {
	for _, tc := range []struct {
		accel string
		want  string
	}{
		{AccelVideoToolbox, "videotoolbox"},
		{AccelNVENC, "nvenc"},
		{AccelVAAPI, "vaapi"},
		{AccelQSV, "qsv"},
		{AccelNone, ""},
	} {
		if got := plexHWEncoderName(tc.accel); got != tc.want {
			t.Errorf("plexHWEncoderName(%q) = %q, want %q", tc.accel, got, tc.want)
		}
	}

	wantQSVDecode := "vaapi"
	if runtime.GOOS == "windows" {
		wantQSVDecode = "d3d11va"
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-hwaccel", "videotoolbox", "-hwaccel_output_format", "videotoolbox_vld"}, "videotoolbox"},
		{[]string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"}, "nvdec"},
		{[]string{"-hwaccel", "vaapi", "-hwaccel_device", "/dev/dri/renderD128"}, "vaapi"},
		{[]string{"-hwaccel", "qsv", "-hwaccel_output_format", "qsv"}, wantQSVDecode},
		{[]string{"-threads", "0"}, ""},
	} {
		if got := plexHWDecoderName(tc.args); got != tc.want {
			t.Errorf("plexHWDecoderName(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}
