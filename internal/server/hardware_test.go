package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeFakeFFmpeg(t *testing.T, logPath string) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "ffmpeg.sh")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + logPath + "\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func lastCommand(t *testing.T, logPath string) string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	return lines[len(lines)-1]
}

func TestProbeVulkanSkipsDevicesWithoutVideo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ffmpeg is a shell script")
	}
	t.Setenv("TMPDIR", t.TempDir())
	script := filepath.Join(t.TempDir(), "ffmpeg.sh")
	body := `#!/bin/sh
case "$*" in
  *testsrc2*) exit 0 ;;
  *vk:0*nullsrc*) exit 1 ;;
  *nullsrc*) exit 0 ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	encoders := " V..... h264_vulkan  H.264 (Vulkan)\n" +
		" V..... hevc_vulkan  HEVC (Vulkan)\n" +
		" V..... av1_vulkan   AV1 (Vulkan)\n"

	cap, ok := probeVulkan(script, encoders, "")
	if !ok {
		t.Fatal("probeVulkan did not find the working device")
	}
	if cap.Device != "1" {
		t.Fatalf("device = %q, want the second index", cap.Device)
	}
	if strings.Join(cap.Encoders, ",") != "h264,hevc,av1" {
		t.Fatalf("encoders = %v", cap.Encoders)
	}
}

func TestHardwareEncodeWorksVulkanArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ffmpeg is a shell script")
	}
	logPath := filepath.Join(t.TempDir(), "ffmpeg.log")
	ffmpeg := writeFakeFFmpeg(t, logPath)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !hardwareEncodeWorks(ctx, ffmpeg, "vulkan", "0", "h264", "h264_vulkan") {
		t.Fatal("fake ffmpeg should have succeeded")
	}
	got := lastCommand(t, logPath)
	for _, want := range []string{
		"-init_hw_device vulkan=vk:0",
		"-filter_hw_device vk",
		"-vf format=nv12,hwupload",
		"-c:v h264_vulkan",
		"-f null -",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

func TestHardwareDecodeWorksVulkanArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ffmpeg is a shell script")
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	sampleDir := filepath.Join(tmp, "nuvio-hwprobe")
	if err := os.MkdirAll(sampleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sample := filepath.Join(sampleDir, "probe-h264.mp4")
	if err := os.WriteFile(sample, []byte("sample"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "ffmpeg.log")
	ffmpeg := writeFakeFFmpeg(t, logPath)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !hardwareDecodeWorks(ctx, ffmpeg, "vulkan", "0", videoCodecProbes[0], "", "") {
		t.Fatal("fake ffmpeg should have succeeded")
	}
	got := lastCommand(t, logPath)
	for _, want := range []string{
		"-init_hw_device vulkan=vk:0",
		"-hwaccel vulkan",
		"-hwaccel_output_format vulkan",
		"-i " + sample,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}
