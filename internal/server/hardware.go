package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"nuvio/internal/jellyfin"
)

// videoCodecProbe describes one output codec and how it is named on each
// backend. Software lists are ordered by preference.
type videoCodecProbe struct {
	Codec    string
	VAAPI    string
	QSV      string
	Vulkan   string
	Software []string
	// Sample lists software encoders that can synthesise a probe clip for the
	// decode test. Hardware-only encoders are deliberately absent.
	Sample []string
}

var videoCodecProbes = []videoCodecProbe{
	{
		Codec:    "h264",
		VAAPI:    "h264_vaapi",
		QSV:      "h264_qsv",
		Vulkan:   "h264_vulkan",
		Software: []string{"libx264", "libopenh264"},
		Sample:   []string{"libx264", "libopenh264"},
	},
	{
		Codec:    "hevc",
		VAAPI:    "hevc_vaapi",
		QSV:      "hevc_qsv",
		Vulkan:   "hevc_vulkan",
		Software: []string{"libx265"},
		Sample:   []string{"libx265"},
	},
	{
		Codec:    "av1",
		VAAPI:    "av1_vaapi",
		QSV:      "av1_qsv",
		Vulkan:   "av1_vulkan",
		Software: []string{"libsvtav1", "libaom-av1", "librav1e"},
		Sample:   []string{"libsvtav1", "libaom-av1", "librav1e"},
	},
	{
		Codec:    "vp9",
		VAAPI:    "vp9_vaapi",
		QSV:      "vp9_qsv",
		Software: []string{"libvpx-vp9"},
		Sample:   []string{"libvpx-vp9"},
	},
}

// hardwareInfo is the result of probing ffmpeg for the encoders and decoders
// that actually work, not just the ones it was compiled with.
type hardwareInfo struct {
	Capabilities []jellyfin.HardwareCapability
	Software     map[string]string
}

// hwProbeTimeout bounds one backend. Probing a broken render node can hang, so
// the whole group is capped rather than each individual codec.
const hwProbeTimeout = 25 * time.Second

func probeHardware(ffmpeg, encoders, decoders string) hardwareInfo {
	info := hardwareInfo{Software: map[string]string{}}
	if ffmpeg == "" {
		return info
	}

	for _, p := range videoCodecProbes {
		for _, name := range p.Software {
			if codecListed(encoders, name) {
				info.Software[p.Codec] = name
				break
			}
		}
	}

	software := make([]string, 0, len(videoCodecProbes))
	for _, p := range videoCodecProbes {
		if info.Software[p.Codec] != "" {
			software = append(software, p.Codec)
		}
	}
	if len(software) > 0 {
		info.Capabilities = append(info.Capabilities, jellyfin.HardwareCapability{
			Backend:  "software",
			Encoders: software,
		})
	}

	device := detectRenderDevice()
	if device != "" {
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, backend := range []string{"vaapi", "qsv"} {
			wg.Add(1)
			go func(backend string) {
				defer wg.Done()
				if cap, ok := probeBackend(ffmpeg, backend, device, encoders, decoders); ok {
					mu.Lock()
					info.Capabilities = append(info.Capabilities, cap)
					mu.Unlock()
				}
			}(backend)
		}
		wg.Wait()
	}
	if cap, ok := probeVulkan(ffmpeg, encoders, decoders); ok {
		info.Capabilities = append(info.Capabilities, cap)
	}
	return info
}

// probeVulkan picks the first Vulkan physical device that can actually encode
// or decode video. Vulkan selects devices by index, not by a DRM path, and a
// machine may expose a display GPU without video queues before the useful one.
func probeVulkan(ffmpeg, encoders, decoders string) (jellyfin.HardwareCapability, bool) {
	hasEncoder := false
	for _, p := range videoCodecProbes {
		if p.Vulkan != "" && codecListed(encoders, p.Vulkan) {
			hasEncoder = true
			break
		}
	}
	if !hasEncoder {
		return jellyfin.HardwareCapability{}, false
	}
	for index := 0; index < 4; index++ {
		device := strconv.Itoa(index)
		if !vulkanDeviceWorks(ffmpeg, device) {
			continue
		}
		if cap, ok := probeBackend(ffmpeg, "vulkan", device, encoders, decoders); ok {
			return cap, true
		}
	}
	return jellyfin.HardwareCapability{}, false
}

func vulkanDeviceWorks(ffmpeg, device string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return runProbe(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error",
		"-init_hw_device", "vulkan=vk:"+device,
		"-f", "lavfi", "-i", "nullsrc",
		"-frames:v", "1", "-f", "null", "-")
}

func probeBackend(ffmpeg, backend, device, encoders, decoders string) (jellyfin.HardwareCapability, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), hwProbeTimeout)
	defer cancel()
	cap := jellyfin.HardwareCapability{Backend: backend, Device: device}
	for _, p := range videoCodecProbes {
		if ctx.Err() != nil {
			break
		}
		name := p.VAAPI
		switch backend {
		case "qsv":
			name = p.QSV
		case "vulkan":
			name = p.Vulkan
		}
		if name != "" && codecListed(encoders, name) && hardwareEncodeWorks(ctx, ffmpeg, backend, device, p.Codec, name) {
			cap.Encoders = append(cap.Encoders, p.Codec)
		}
		if hardwareDecodeWorks(ctx, ffmpeg, backend, device, p, encoders, decoders) {
			cap.Decoders = append(cap.Decoders, p.Codec)
		}
	}
	if len(cap.Encoders) == 0 && len(cap.Decoders) == 0 {
		return cap, false
	}
	return cap, true
}

// hardwareEncodeWorks encodes a single frame through the vendor encoder. Node
// presence is not enough: a missing user-space driver fails here.
func hardwareEncodeWorks(ctx context.Context, ffmpeg, backend, device, codec, encoder string) bool {
	args := []string{"-hide_banner", "-loglevel", "error"}
	switch backend {
	case "vaapi":
		args = append(args, "-vaapi_device", device)
	case "qsv":
		args = append(args,
			"-qsv_device", device,
			"-init_hw_device", "qsv=hw",
			"-filter_hw_device", "hw",
		)
	case "vulkan":
		args = append(args,
			"-init_hw_device", "vulkan=vk:"+device,
			"-filter_hw_device", "vk",
		)
	}
	args = append(args,
		"-f", "lavfi", "-i", "testsrc2=size=160x120:rate=1",
		"-frames:v", "1",
	)
	switch backend {
	case "vaapi":
		args = append(args, "-vf", "format=nv12,hwupload")
	case "qsv":
		args = append(args, "-vf", "format=nv12,hwupload=extra_hw_frames=64,format=qsv")
	case "vulkan":
		args = append(args, "-vf", "format=nv12,hwupload")
	}
	args = append(args, "-c:v", encoder, "-f", "null", "-")
	return runProbe(ctx, ffmpeg, args...)
}

// hardwareDecodeWorks decodes a small clip generated with a software encoder.
// When no software encoder is available the decoder list is used as a weaker
// fallback so the capability is still reported on minimal ffmpeg builds.
func hardwareDecodeWorks(ctx context.Context, ffmpeg, backend, device string, probe videoCodecProbe, encoders, decoders string) bool {
	sample := probeSample(ffmpeg, probe, encoders)
	if sample == "" {
		// Vulkan decoders are not listed separately; without a sample there
		// is no way to prove the hardware path works, so do not report it.
		switch backend {
		case "vaapi":
			return codecListed(decoders, probe.VAAPI)
		case "qsv":
			return codecListed(decoders, probe.QSV)
		default:
			return false
		}
	}
	args := []string{"-hide_banner", "-loglevel", "error"}
	switch backend {
	case "vaapi":
		args = append(args, "-hwaccel", "vaapi", "-hwaccel_device", device)
	case "qsv":
		args = append(args, "-hwaccel", "qsv", "-qsv_device", device)
	case "vulkan":
		args = append(args,
			"-init_hw_device", "vulkan=vk:"+device,
			"-hwaccel", "vulkan",
			"-hwaccel_output_format", "vulkan",
		)
	}
	args = append(args, "-i", sample, "-frames:v", "1", "-f", "null", "-")
	return runProbe(ctx, ffmpeg, args...)
}

// sampleMu serialises probe clip generation: both backend probes run
// concurrently and would otherwise race on the same cache file.
var sampleMu sync.Mutex

// probeSample returns a cached one-frame clip in the requested codec, or ""
// when ffmpeg has no software encoder for it.
func probeSample(ffmpeg string, probe videoCodecProbe, encoders string) string {
	sampleMu.Lock()
	defer sampleMu.Unlock()
	dir := filepath.Join(os.TempDir(), "nuvio-hwprobe")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	ext := ".mp4"
	if probe.Codec == "vp9" {
		ext = ".webm"
	}
	path := filepath.Join(dir, "probe-"+probe.Codec+ext)
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		return path
	}
	encoder := ""
	for _, name := range probe.Sample {
		if codecListed(encoders, name) {
			encoder = name
			break
		}
	}
	if encoder == "" {
		return ""
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=160x120:rate=1",
		"-frames:v", "1", "-c:v", encoder, "-pix_fmt", "yuv420p",
	}
	switch encoder {
	case "libx264":
		args = append(args, "-preset", "ultrafast")
	case "libx265":
		args = append(args, "-preset", "ultrafast", "-x265-params", "log-level=error")
	case "libsvtav1":
		args = append(args, "-preset", "13")
	case "libaom-av1":
		args = append(args, "-cpu-used", "8", "-usage", "realtime")
	case "librav1e":
		args = append(args, "-speed", "10")
	case "libvpx-vp9":
		args = append(args, "-deadline", "realtime", "-cpu-used", "8")
	}
	args = append(args, path)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if !runProbe(ctx, ffmpeg, args...) {
		_ = os.Remove(path)
		return ""
	}
	if st, err := os.Stat(path); err != nil || st.Size() == 0 {
		return ""
	}
	return path
}

func runProbe(ctx context.Context, ffmpeg string, args ...string) bool {
	return exec.CommandContext(ctx, ffmpeg, args...).Run() == nil
}

// codecListed reports whether ffmpeg lists name as an encoder or decoder.
// Columns are "flags name description", so the name is the second field.
func codecListed(out, name string) bool {
	if name == "" {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == name {
			return true
		}
	}
	return false
}

func detectRenderDevice() string {
	for i := 128; i < 140; i++ {
		p := fmt.Sprintf("/dev/dri/renderD%d", i)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// applyHardware stores the probe result on the API and logs a summary.
func applyHardware(api *jellyfin.API, ffmpeg string) hardwareInfo {
	encoders, decoders := "", ""
	if ffmpeg != "" {
		encoders = ffmpegList(ffmpeg, "-encoders")
		decoders = ffmpegList(ffmpeg, "-decoders")
	}
	info := probeHardware(ffmpeg, encoders, decoders)
	api.SetEncoder(detectEncoder(encoders))
	api.SetSoftwareEncoders(info.Software)
	api.SetCapabilities(info.Capabilities)
	vaapi, qsv, vulkan := "", "", ""
	for _, cap := range info.Capabilities {
		if len(cap.Encoders) == 0 {
			continue
		}
		switch cap.Backend {
		case "vaapi":
			vaapi = cap.Device
		case "qsv":
			qsv = cap.Device
		case "vulkan":
			vulkan = cap.Device
		}
	}
	api.SetHardware(vaapi, qsv, vulkan)
	for _, cap := range info.Capabilities {
		if cap.Backend == "software" {
			log.Printf("Hardware acceleration: software only (%s)", strings.Join(cap.Encoders, ", "))
			continue
		}
		log.Printf("Hardware acceleration: %s (%s) encode %s / decode %s",
			cap.Backend, cap.Device, joinOrNone(cap.Encoders), joinOrNone(cap.Decoders))
	}
	if vaapi == "" && qsv == "" && vulkan == "" {
		log.Printf("Hardware acceleration: not available, using software")
	}
	return info
}

func joinOrNone(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}

// detectEncoder returns the preferred software H.264 encoder. It is kept for
// the admin overview and as the last-resort fallback when a newer codec has no
// working software encoder.
func detectEncoder(encoders string) string {
	if encoders == "" {
		return ""
	}
	for _, name := range []string{"libx264", "libopenh264", "h264_v4l2m2m"} {
		if codecListed(encoders, name) {
			return name
		}
	}
	return "libx264"
}

func ffmpegList(ffmpeg, flag string) string {
	out, err := exec.Command(ffmpeg, "-hide_banner", flag).Output()
	if err != nil {
		return ""
	}
	return string(out)
}
