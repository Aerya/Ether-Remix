package transcode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type qsvToneMapMethod string

const (
	qsvToneMapNone   qsvToneMapMethod = ""
	qsvToneMapVPP    qsvToneMapMethod = "vpp"
	qsvToneMapOpenCL qsvToneMapMethod = "opencl"

	qsvToneMapAdmissionBudget = 3 * time.Second
	qsvToneMapProbeTimeout    = 8 * time.Second
)

type qsvToneMapper interface {
	method(hw core.HWAccel, f features, wait bool) qsvToneMapMethod
	status(hw core.HWAccel, f features) (available, known bool)
}

type qsvToneMapKey struct {
	device  string
	driver  string
	encoder string
	vpp     bool
	opencl  bool
}

// qsvToneMapGraph builds the QSV hardware HDR-to-SDR input and filter arguments
// shared by runtime playback and synthetic per-device validation.
func qsvToneMapGraph(method qsvToneMapMethod, device string, w, h int) (input, filters []string, ok bool) {
	if w <= 0 || h <= 0 {
		return nil, nil, false
	}
	switch method {
	case qsvToneMapVPP:
		return []string{"-hwaccel", "qsv", "-hwaccel_output_format", "qsv"},
			[]string{fmt.Sprintf("vpp_qsv=w=%d:h=%d:format=nv12:tonemap=1", w, h)},
			true
	case qsvToneMapOpenCL:
		dev := device
		if dev == "" {
			dev = DefaultVAAPIDevice
		}
		return []string{
				"-init_hw_device", "vaapi=va:" + dev,
				"-init_hw_device", "qsv=qs@va",
				"-init_hw_device", "opencl=ocl@va",
				"-filter_hw_device", "ocl",
				"-hwaccel", "qsv",
				"-hwaccel_output_format", "qsv",
			},
			[]string{
				fmt.Sprintf("vpp_qsv=w=%d:h=%d:format=p010le", w, h),
				"hwmap=derive_device=opencl",
				"tonemap_opencl=tonemap=hable:desat=0:format=nv12:t=bt709:m=bt709:p=bt709",
				"hwmap=derive_device=qsv:reverse=1:extra_hw_frames=16",
			},
			true
	default:
		return nil, nil, false
	}
}

func qsvCandidates(hw core.HWAccel, f features) (vpp, opencl bool, encoder string) {
	if hw.Name != AccelQSV || hw.NoGPUTonemap || !f.hwaccels["qsv"] || !f.hasFilter("vpp_qsv") {
		return false, false, ""
	}
	switch {
	case contains(hw.Encoders, "h264_qsv"):
		encoder = "h264_qsv"
	case contains(hw.Encoders, "hevc_qsv"):
		encoder = "hevc_qsv"
	default:
		return false, false, ""
	}
	vpp = f.vppQSVTonemap
	opencl = f.hwaccels["vaapi"] && f.hwaccels["opencl"] && f.hasFilter("hwmap") && f.hasFilter("tonemap_opencl")
	return vpp, opencl, encoder
}

func qsvHardwareToneMapAvailable(f features, hw core.HWAccel) bool {
	vpp, opencl, _ := qsvCandidates(hw, f)
	if (!vpp && !opencl) || f.qsvToneMap == nil {
		return false
	}
	avail, known := f.qsvToneMap.status(hw, f)
	return known && avail
}

func qsvToneMapPendingCandidate(f features, hw core.HWAccel) bool {
	vpp, opencl, _ := qsvCandidates(hw, f)
	if (!vpp && !opencl) || f.qsvToneMap == nil {
		return false
	}
	_, known := f.qsvToneMap.status(hw, f)
	return !known
}

func qsvDeviceDriverFingerprint(device string) string {
	var parts []string
	if device != "" {
		devDir := filepath.Join(sysfsDRM, filepath.Base(device), "device")
		for _, field := range []string{"vendor", "device", "revision"} {
			if b, err := os.ReadFile(filepath.Join(devDir, field)); err == nil {
				if v := strings.TrimSpace(string(b)); v != "" {
					parts = append(parts, v)
				}
			}
		}
		if link, err := os.Readlink(filepath.Join(devDir, "driver")); err == nil {
			drv := filepath.Base(link)
			parts = append(parts, drv)
			if b, err := os.ReadFile(filepath.Join("/sys/module", drv, "srcversion")); err == nil {
				if v := strings.TrimSpace(string(b)); v != "" {
					parts = append(parts, v)
				}
			}
		}
	}
	if va := strings.TrimSpace(os.Getenv("LIBVA_DRIVER_NAME")); va != "" {
		parts = append(parts, "libva="+va)
	}
	return strings.Join(parts, ":")
}

func sourceQSVToneMapKey(hw core.HWAccel, f features) (qsvToneMapKey, bool) {
	vpp, opencl, enc := qsvCandidates(hw, f)
	if !vpp && !opencl {
		return qsvToneMapKey{}, false
	}
	return qsvToneMapKey{
		device:  hw.Device,
		driver:  qsvDeviceDriverFingerprint(hw.Device),
		encoder: enc,
		vpp:     vpp,
		opencl:  opencl,
	}, true
}

// qsvToneMapCache lazily validates Intel QSV HDR-to-SDR tone-mapping per device
// and driver on first demand, persisting the winning graph (or software fallback
// reason) in transcode_decode_caps. No work runs at startup.
type qsvToneMapCache struct {
	mu                   sync.Mutex
	results              map[qsvToneMapKey]qsvToneMapMethod
	pending              map[qsvToneMapKey]chan struct{}
	reasons              map[qsvToneMapKey]string
	logged               map[qsvToneMapKey]bool
	pendingLogged        map[qsvToneMapKey]bool
	db                   *sql.DB
	log                  *slog.Logger
	onChange             func()
	build                string
	buildOnce            sync.Once
	queue                []qsvToneMapKey
	running              bool
	ctx                  context.Context
	cancel               context.CancelFunc
	wg                   sync.WaitGroup
	runner               Runner
	ffmpeg, ffprobe, dir string
	encoders             map[string]bool
}

func newQSVToneMapCache(r Runner, ffmpeg, ffprobe, dir string, encoders map[string]bool) *qsvToneMapCache {
	ctx, cancel := context.WithCancel(context.Background())
	return &qsvToneMapCache{
		results:       make(map[qsvToneMapKey]qsvToneMapMethod),
		pending:       make(map[qsvToneMapKey]chan struct{}),
		reasons:       make(map[qsvToneMapKey]string),
		logged:        make(map[qsvToneMapKey]bool),
		pendingLogged: make(map[qsvToneMapKey]bool),
		ctx:           ctx,
		cancel:        cancel,
		runner:        r,
		ffmpeg:        ffmpeg,
		ffprobe:       ffprobe,
		dir:           dir,
		encoders:      encoders,
	}
}

func (c *qsvToneMapCache) status(hw core.HWAccel, f features) (bool, bool) {
	k, ok := sourceQSVToneMapKey(hw, f)
	if !ok {
		return false, true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m, known := c.results[k]
	if !known {
		return false, false
	}
	return m != qsvToneMapNone, true
}

func (c *qsvToneMapCache) enqueue(k qsvToneMapKey) (qsvToneMapMethod, bool, chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, known := c.results[k]; known {
		return m, true, nil
	}
	if done := c.pending[k]; done != nil {
		return qsvToneMapNone, false, done
	}
	if c.ctx.Err() != nil || len(c.results)+len(c.pending) >= 32 || len(c.queue) >= 8 {
		return qsvToneMapNone, false, nil
	}
	done := make(chan struct{})
	c.pending[k] = done
	c.queue = append(c.queue, k)
	if !c.running {
		c.running = true
		c.wg.Add(1)
		go c.work()
	}
	return qsvToneMapNone, false, done
}

func (c *qsvToneMapCache) method(hw core.HWAccel, f features, wait bool) qsvToneMapMethod {
	k, ok := sourceQSVToneMapKey(hw, f)
	if !ok {
		return qsvToneMapNone
	}
	m, known, done := c.enqueue(k)
	if known || !wait || done == nil {
		return m
	}
	timer := time.NewTimer(qsvToneMapAdmissionBudget)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		c.mu.Lock()
		if !c.pendingLogged[k] && c.log != nil {
			c.pendingLogged[k] = true
			c.log.Info("QSV hardware tone-map probe pending", "device", k.device, "reason", "admission deadline exceeded; software tone-map for this run")
		}
		c.mu.Unlock()
		return qsvToneMapNone
	case <-c.ctx.Done():
		return qsvToneMapNone
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.results[k]
}

func (c *qsvToneMapCache) close() {
	c.mu.Lock()
	c.cancel()
	c.mu.Unlock()
	c.wg.Wait()
}

func (c *qsvToneMapCache) identifyBuild() {
	if c.db == nil {
		return
	}
	f, err := os.Open(c.ffmpeg)
	if err != nil {
		return
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err == nil {
		c.mu.Lock()
		c.build = hex.EncodeToString(h.Sum(nil))
		c.mu.Unlock()
	}
}

func (c *qsvToneMapCache) storeKey(k qsvToneMapKey) string {
	host, _ := os.Hostname()
	b, _ := json.Marshal([]any{host, "qsv_tonemap", k.device, k.driver, k.encoder, k.vpp, k.opencl})
	return string(b)
}

func parseStoredQSVToneMap(supported int, reason string) (qsvToneMapMethod, string, bool) {
	clean := reason
	for strings.HasPrefix(clean, "stored: ") {
		clean = strings.TrimPrefix(clean, "stored: ")
	}
	if supported == 0 {
		return qsvToneMapNone, "stored: " + clean, true
	}
	switch {
	case strings.HasPrefix(clean, string(qsvToneMapVPP)+":"):
		return qsvToneMapVPP, "stored: " + clean, true
	case strings.HasPrefix(clean, string(qsvToneMapOpenCL)+":"):
		return qsvToneMapOpenCL, "stored: " + clean, true
	default:
		return qsvToneMapNone, "", false
	}
}

func (c *qsvToneMapCache) loadResult(k qsvToneMapKey) (qsvToneMapMethod, string, bool) {
	if c.db == nil || c.build == "" {
		return qsvToneMapNone, "", false
	}
	ctx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
	defer cancel()
	var supported int
	var reason string
	err := c.db.QueryRowContext(ctx, `SELECT supported, reason FROM transcode_decode_caps WHERE build=? AND source=?`, c.build, c.storeKey(k)).Scan(&supported, &reason)
	if err != nil {
		return qsvToneMapNone, "", false
	}
	return parseStoredQSVToneMap(supported, reason)
}

func (c *qsvToneMapCache) saveResult(k qsvToneMapKey, m qsvToneMapMethod, reason string) {
	if c.db == nil || c.build == "" {
		return
	}
	for strings.HasPrefix(reason, "stored: ") {
		reason = strings.TrimPrefix(reason, "stored: ")
	}
	supported := 0
	if m != qsvToneMapNone {
		supported = 1
		prefix := string(m) + ": "
		if !strings.HasPrefix(reason, prefix) {
			reason = prefix + reason
		}
	}
	ctx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
	defer cancel()
	_, err := c.db.ExecContext(ctx, `INSERT INTO transcode_decode_caps(build, source, supported, reason) VALUES(?,?,?,?) ON CONFLICT(build, source) DO UPDATE SET supported=excluded.supported, reason=excluded.reason`, c.build, c.storeKey(k), supported, reason)
	if err != nil && c.log != nil {
		c.log.Warn("QSV tone-map result persistence failed", "error", err)
	}
}

func (c *qsvToneMapCache) logResult(k qsvToneMapKey, m qsvToneMapMethod, reason string) {
	if c.logged[k] {
		return
	}
	c.logged[k] = true
	if c.log == nil {
		return
	}
	if m == qsvToneMapNone {
		c.log.Warn("QSV hardware HDR tone-mapping unavailable; keeping software tone-mapping",
			"device", k.device, "driver", k.driver, "reason", reason, "ffmpegBuild", c.build)
		return
	}
	c.log.Info("QSV hardware HDR tone-mapping validated",
		"method", string(m), "device", k.device, "driver", k.driver, "reason", reason, "ffmpegBuild", c.build)
}

func (c *qsvToneMapCache) work() {
	defer c.wg.Done()
	for {
		c.mu.Lock()
		if len(c.queue) == 0 || c.ctx.Err() != nil {
			c.running = false
			c.mu.Unlock()
			return
		}
		k := c.queue[0]
		c.queue = c.queue[1:]
		c.mu.Unlock()

		c.buildOnce.Do(c.identifyBuild)

		m, reason, known := c.loadResult(k)
		if !known {
			m, reason = c.probe(k)
		}

		c.mu.Lock()
		c.results[k] = m
		c.reasons[k] = reason
		c.logResult(k, m, reason)
		onChange := c.onChange
		c.mu.Unlock()

		if !known {
			c.saveResult(k, m, reason)
		}

		c.mu.Lock()
		if done := c.pending[k]; done != nil {
			close(done)
			delete(c.pending, k)
		}
		c.mu.Unlock()

		if onChange != nil {
			onChange()
		}
	}
}

func (c *qsvToneMapCache) probe(k qsvToneMapKey) (qsvToneMapMethod, string) {
	sampleEnc := ""
	switch {
	case c.encoders["libx265"]:
		sampleEnc = "libx265"
	case c.encoders["hevc_qsv"]:
		sampleEnc = "hevc_qsv"
	default:
		return qsvToneMapNone, "missing HEVC Main10 encoder for synthetic HDR sample"
	}

	dir, err := os.MkdirTemp(c.dir, "qsv-tonemap-probe-")
	if err != nil {
		return qsvToneMapNone, "cannot create tone-map probe directory"
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(c.ctx, qsvToneMapProbeTimeout)
	defer cancel()

	runFFmpeg := func(args []string) (string, bool) {
		var stderr bytes.Buffer
		err := c.runner.Run(ctx, Command{
			Path:       c.ffmpeg,
			Args:       append([]string{"-hide_banner", "-nostdin", "-loglevel", "error", "-threads", "1", "-filter_threads", "1"}, args...),
			Stderr:     &stderr,
			Background: true,
		})
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 400 {
			detail = detail[len(detail)-400:]
		}
		if err != nil {
			if detail == "" {
				detail = err.Error()
			}
			return detail, false
		}
		return "", true
	}

	sample := filepath.Join(dir, "sample-hdr10.mkv")
	srcFilter := "color=c=gray:size=160x90:rate=4:duration=1,format=yuv420p10le,setparams=range=tv:color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc"
	sampleArgs := []string{"-f", "lavfi", "-i", srcFilter, "-an"}
	if sampleEnc == "libx265" {
		sampleArgs = append(sampleArgs,
			"-c:v", "libx265",
			"-preset", "ultrafast",
			"-pix_fmt", "yuv420p10le",
			"-profile:v", "main10",
			"-color_primaries", "bt2020",
			"-color_trc", "smpte2084",
			"-colorspace", "bt2020nc",
			"-x265-params", "pools=none:frame-threads=1:log-level=error:hdr10=1:repeat-headers=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1):max-cll=1000,400",
		)
	} else {
		if k.device != "" {
			sampleArgs = append([]string{"-qsv_device", k.device}, sampleArgs...)
		}
		sampleArgs = append(sampleArgs,
			"-vf", "format=p010le",
			"-c:v", "hevc_qsv",
			"-profile:v", "main10",
			"-color_primaries", "bt2020",
			"-color_trc", "smpte2084",
			"-colorspace", "bt2020nc",
		)
	}
	sampleArgs = append(sampleArgs, "-y", sample)
	if detail, ok := runFFmpeg(sampleArgs); !ok {
		return qsvToneMapNone, "synthetic PQ sample encode failed: " + detail
	}

	var candidates []qsvToneMapMethod
	if k.vpp {
		candidates = append(candidates, qsvToneMapVPP)
	}
	if k.opencl {
		candidates = append(candidates, qsvToneMapOpenCL)
	}

	var failures []string
	for _, candidate := range candidates {
		input, filters, ok := qsvToneMapGraph(candidate, k.device, 160, 90)
		if !ok {
			continue
		}
		outPath := filepath.Join(dir, "out-"+string(candidate)+".mkv")
		var args []string
		if k.device != "" && !contains(input, "-init_hw_device") {
			args = append(args, "-qsv_device", k.device)
		}
		args = append(args, input...)
		args = append(args,
			"-i", sample,
			"-an",
			"-filter_complex", "[0:0]"+strings.Join(filters, ",")+"[vout]",
			"-map", "[vout]",
			"-c:v", k.encoder,
		)
		if k.encoder == "h264_qsv" {
			args = append(args, "-profile:v", "high")
		}
		args = append(args, "-y", outPath)

		if detail, runOK := runFFmpeg(args); !runOK {
			failures = append(failures, string(candidate)+": "+detail)
			continue
		}
		if reason, valid := c.verifySDROutput(ctx, outPath); !valid {
			failures = append(failures, string(candidate)+": "+reason)
			continue
		}
		note := string(candidate) + ": synthetic 10-bit PQ -> 8-bit SDR BT.709 probe passed"
		if len(failures) > 0 {
			note += " (after " + strings.Join(failures, "; ") + ")"
		}
		return candidate, note
	}
	if len(failures) == 0 {
		return qsvToneMapNone, "no QSV hardware tone-map filters in FFmpeg build"
	}
	return qsvToneMapNone, strings.Join(failures, "; ")
}

func (c *qsvToneMapCache) verifySDROutput(ctx context.Context, outPath string) (string, bool) {
	var stdout bytes.Buffer
	err := c.runner.Run(ctx, Command{
		Path:       c.ffprobe,
		Args:       []string{"-v", "error", "-select_streams", "v:0", "-show_entries", "stream=codec_name,pix_fmt,bits_per_raw_sample,color_space,color_transfer,color_primaries", "-of", "json", outPath},
		Stdout:     &stdout,
		Background: true,
	})
	if err != nil {
		return "output verification ffprobe failed", false
	}
	var probed struct {
		Streams []struct {
			Codec          string `json:"codec_name"`
			PixFmt         string `json:"pix_fmt"`
			BitsPerSample  string `json:"bits_per_raw_sample"`
			ColorSpace     string `json:"color_space"`
			ColorTransfer  string `json:"color_transfer"`
			ColorPrimaries string `json:"color_primaries"`
		} `json:"streams"`
	}
	if json.Unmarshal(stdout.Bytes(), &probed) != nil || len(probed.Streams) != 1 {
		return "output verification JSON invalid", false
	}
	st := probed.Streams[0]
	if st.PixFmt != "yuv420p" && st.PixFmt != "nv12" {
		return fmt.Sprintf("unexpected output pix_fmt %q (want 8-bit yuv420p/nv12)", st.PixFmt), false
	}
	if st.BitsPerSample != "" && st.BitsPerSample != "0" && st.BitsPerSample != "8" {
		return fmt.Sprintf("unexpected output bit depth %q (want 8)", st.BitsPerSample), false
	}
	if st.ColorTransfer != "bt709" || st.ColorPrimaries != "bt709" || st.ColorSpace != "bt709" {
		return fmt.Sprintf("unexpected output colour %s/%s/%s (want bt709/bt709/bt709)", st.ColorPrimaries, st.ColorTransfer, st.ColorSpace), false
	}
	return "", true
}
