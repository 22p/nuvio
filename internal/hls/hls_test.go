package hls

import (
	"strings"
	"testing"
)

func TestBuildArgsSoftware(t *testing.T) {
	args := buildArgs(Options{
		BitrateKbps: 4000,
		Height:      720,
		Preset:      "veryfast",
		Encoder:     "libx264",
	}, "/media/movie.mkv", "/tmp/session")
	got := strings.Join(args, " ")

	if !strings.Contains(got, "-c:v libx264") {
		t.Fatalf("software encoder missing: %s", got)
	}
	if !strings.Contains(got, "-preset veryfast") {
		t.Fatalf("software preset missing: %s", got)
	}
	if !strings.Contains(got, "-vf scale=-2:720") {
		t.Fatalf("software scale missing: %s", got)
	}
	if strings.Contains(got, "-hwaccel") {
		t.Fatalf("software session must not use hwaccel: %s", got)
	}
}

func TestBuildArgsVAAPI(t *testing.T) {
	args := buildArgs(Options{
		BitrateKbps:    4000,
		Height:         1080,
		Hardware:       "vaapi",
		HardwareDevice: "/dev/dri/renderD128",
	}, "/media/movie.mkv", "/tmp/session")
	got := strings.Join(args, " ")

	for _, want := range []string{
		"-hwaccel vaapi",
		"-hwaccel_device /dev/dri/renderD128",
		"-hwaccel_output_format vaapi",
		"-c:v h264_vaapi",
		"-vf scale_vaapi=w=-2:h=1080:format=nv12",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "libx264") {
		t.Fatalf("hardware session must not use libx264: %s", got)
	}
}

func TestBuildArgsQSV(t *testing.T) {
	args := buildArgs(Options{
		BitrateKbps:    4000,
		Width:          1281,
		Height:         721,
		Hardware:       "qsv",
		HardwareDevice: "/dev/dri/renderD128",
		VideoCodec:     "h264",
	}, "/media/movie.mkv", "/tmp/session")
	got := strings.Join(args, " ")

	if strings.Index(got, "-c:v h264_qsv") > strings.Index(got, "-i /media/movie.mkv") {
		t.Fatalf("QSV decoder must be selected before the input: %s", got)
	}
	for _, want := range []string{
		"-hwaccel qsv",
		"-qsv_device /dev/dri/renderD128",
		"-hwaccel_output_format qsv",
		"-c:v h264_qsv",
		"-vf scale_qsv=w=1280:h=720:format=nv12",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "libx264") || strings.Contains(got, "vaapi") {
		t.Fatalf("QSV session mixed encoders: %s", got)
	}
}

func TestBuildArgsCopyIgnoresHardware(t *testing.T) {
	args := buildArgs(Options{
		BitrateKbps:    4000,
		Height:         720,
		Hardware:       "vaapi",
		HardwareDevice: "/dev/dri/renderD128",
		Copy:           true,
	}, "/media/movie.mkv", "/tmp/session")
	got := strings.Join(args, " ")

	if !strings.Contains(got, "-c copy") {
		t.Fatalf("copy mode missing: %s", got)
	}
	if strings.Contains(got, "-hwaccel") {
		t.Fatalf("copy mode must not decode through VAAPI: %s", got)
	}
}

func TestBuildArgsAV1Software(t *testing.T) {
	args := buildArgs(Options{
		BitrateKbps: 4000,
		Height:      1080,
		Codec:       "av1",
		Encoder:     "libsvtav1",
	}, "/media/movie.mkv", "/tmp/session")
	got := strings.Join(args, " ")

	for _, want := range []string{
		"-c:v libsvtav1",
		"-tag:v av01",
		"-hls_segment_type fmp4",
		"-hls_segment_filename /tmp/session/seg%05d.m4s",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "-maxrate") {
		t.Fatalf("SVT-AV1 rejects maxrate without CRF: %s", got)
	}
	if strings.Contains(got, "seg%05d.ts") {
		t.Fatalf("AV1 cannot be muxed into MPEG-TS: %s", got)
	}
}

func TestBuildArgsHEVCVAAPI(t *testing.T) {
	args := buildArgs(Options{
		BitrateKbps:    4000,
		Height:         1080,
		Hardware:       "vaapi",
		HardwareDevice: "/dev/dri/renderD128",
		Codec:          "hevc",
	}, "/media/movie.mkv", "/tmp/session")
	got := strings.Join(args, " ")

	for _, want := range []string{
		"-c:v hevc_vaapi",
		"-profile:v main",
		"-tag:v hvc1",
		"-hls_segment_type fmp4",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "profile:v high") {
		t.Fatalf("HEVC must not use the H.264 high profile: %s", got)
	}
}

func TestNormalizeCodec(t *testing.T) {
	cases := map[string]string{
		"":      "h264",
		"H264":  "h264",
		"h265":  "hevc",
		"hvc1":  "hevc",
		"av01":  "av1",
		"VP9":   "vp9",
		"mpeg2": "h264",
	}
	for in, want := range cases {
		if got := NormalizeCodec(in); got != want {
			t.Fatalf("NormalizeCodec(%q) = %q, want %q", in, got, want)
		}
	}
}
