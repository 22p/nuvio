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
