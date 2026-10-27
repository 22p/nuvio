// Package media turns files on disk into catalog items. It avoids external
// metadata plugins entirely: titles come from filenames, artwork from sidecar
// files or generated art, and technical metadata from ffprobe when available
// (with a built-in MP4 fallback so the server works without ffmpeg installed).
package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nuvio/internal/model"
)

// VideoExtensions is the set of files treated as playable media.
var VideoExtensions = map[string]bool{
	".mp4": true, ".m4v": true, ".mkv": true, ".webm": true, ".avi": true,
	".mov": true, ".ts": true, ".m2ts": true, ".wmv": true, ".flv": true,
	".ogv": true, ".mpg": true, ".mpeg": true,
}

// SubtitleExtensions is the set of supported sidecar subtitles.
var SubtitleExtensions = map[string]bool{
	".srt": true, ".vtt": true, ".ass": true, ".ssa": true,
}

// ContainerFor maps a file extension to a Jellyfin container name.
func ContainerFor(path string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch ext {
	case "m4v", "mov":
		return "mp4"
	case "mkv":
		return "mkv"
	case "webm":
		return "webm"
	case "avi":
		return "avi"
	case "m2ts", "ts":
		return "ts"
	}
	return ext
}

// GuessCodecs returns plausible codecs for a container when no probe is
// available. It is only used to build a best-effort media source.
func GuessCodecs(container string) (video, audio string, width, height, channels int) {
	switch container {
	case "mp4":
		return "h264", "aac", 1920, 1080, 2
	case "webm":
		return "vp9", "opus", 1920, 1080, 2
	case "mkv":
		return "h264", "aac", 1920, 1080, 2
	case "avi":
		return "mpeg4", "mp3", 720, 480, 2
	default:
		return "h264", "aac", 0, 0, 2
	}
}

type ffprobeResult struct {
	Streams []struct {
		Index        int               `json:"index"`
		CodecType    string            `json:"codec_type"`
		CodecName    string            `json:"codec_name"`
		Width        int               `json:"width"`
		Height       int               `json:"height"`
		Profile      string            `json:"profile"`
		AvgFrameRate string            `json:"avg_frame_rate"`
		Channels     int               `json:"channels"`
		BitRate      string            `json:"bit_rate"`
		Tags         map[string]string `json:"tags"`
		Disposition  struct {
			Default int `json:"default"`
			Forced  int `json:"forced"`
		} `json:"disposition"`
	} `json:"streams"`
	Format struct {
		Duration   string `json:"duration"`
		Size       string `json:"size"`
		BitRate    string `json:"bit_rate"`
		FormatName string `json:"format_name"`
	} `json:"format"`
}

// ProbeFile fills a MediaInfo for path. ffprobe may be empty, in which case a
// container-based guess and the built-in MP4 duration parser are used.
func ProbeFile(ctx context.Context, path, ffprobe string) model.MediaInfo {
	container := ContainerFor(path)
	info := model.MediaInfo{Container: container}
	if st, err := os.Stat(path); err == nil {
		info.Size = st.Size()
	}
	if ffprobe != "" {
		if out, err := runFFprobe(ctx, ffprobe, path); err == nil {
			return fromFFprobe(out, container)
		}
	}
	video, audio, w, h, channels := GuessCodecs(container)
	info.VideoCodec, info.AudioCodec = video, audio
	info.Width, info.Height, info.AudioChannels = w, h, channels
	info.AudioTracks = []model.AudioTrack{{Index: 1, Codec: audio, Channels: channels, Default: true, Language: "und"}}
	if f, err := os.Open(path); err == nil {
		if d := MP4Duration(f); d > 0 {
			info.DurationTicks = d
		}
		f.Close()
	}
	return info
}

func runFFprobe(ctx context.Context, ffprobe, path string) (*ffprobeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "quiet", "-print_format", "json",
		"-show_format", "-show_streams", path)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var res ffprobeResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func parseFrameRate(s string) float64 {
	if s == "" {
		return 0
	}
	parts := strings.SplitN(s, "/", 2)
	num, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	den := 1.0
	if len(parts) == 2 {
		den, err = strconv.ParseFloat(parts[1], 64)
		if err != nil || den == 0 {
			return 0
		}
	}
	return num / den
}

func fromFFprobe(res *ffprobeResult, container string) model.MediaInfo {
	info := model.MediaInfo{Container: container}
	secs, _ := strconv.ParseFloat(res.Format.Duration, 64)
	info.DurationTicks = int64(secs * 10_000_000)
	info.Size = atoi64(res.Format.Size)
	info.Bitrate = atoi64(res.Format.BitRate)
	for _, s := range res.Streams {
		switch s.CodecType {
		case "video":
			if info.VideoCodec == "" {
				info.VideoCodec = s.CodecName
				info.Width, info.Height = s.Width, s.Height
				info.VideoProfile = s.Profile
				info.FrameRate = parseFrameRate(s.AvgFrameRate)
			}
		case "audio":
			if info.AudioCodec == "" {
				info.AudioCodec = s.CodecName
				info.AudioChannels = s.Channels
			}
			lang := s.Tags["language"]
			if lang == "" {
				lang = "und"
			}
			info.AudioTracks = append(info.AudioTracks, model.AudioTrack{
				Index:    s.Index,
				Language: lang,
				Title:    s.Tags["title"],
				Codec:    s.CodecName,
				Channels: s.Channels,
				Default:  s.Disposition.Default == 1,
			})
		case "subtitle":
			lang := s.Tags["language"]
			if lang == "" {
				lang = "und"
			}
			info.Subtitles = append(info.Subtitles, model.Subtitle{
				Language:    lang,
				Title:       s.Tags["title"],
				Codec:       s.CodecName,
				Embedded:    true,
				StreamIndex: s.Index,
				Forced:      s.Disposition.Forced == 1,
				Default:     s.Disposition.Default == 1,
			})
		}
	}
	if len(info.AudioTracks) == 0 {
		info.AudioTracks = []model.AudioTrack{{Index: 1, Codec: info.AudioCodec, Channels: info.AudioChannels, Default: true, Language: "und"}}
	}
	if info.AudioChannels == 0 {
		info.AudioChannels = 2
	}
	return info
}

// MP4Duration reads the movie header (mvhd) of an MP4/MOV file and returns its
// duration in 100ns ticks. It is deliberately forgiving: unknown layouts return
// zero rather than an error.
func MP4Duration(r io.ReadSeeker) int64 {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0
	}
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil || size <= 0 {
		return 0
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0
	}
	moov, ok := findBox(r, 0, size, "moov", 0)
	if !ok {
		return 0
	}
	mvhd, ok := findBox(r, moov.start, moov.end, "mvhd", 0)
	if !ok {
		return 0
	}
	payload := make([]byte, mvhd.end-mvhd.start)
	if _, err := r.Seek(mvhd.start, io.SeekStart); err != nil {
		return 0
	}
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0
	}
	if len(payload) < 20 {
		return 0
	}
	version := payload[0]
	var timescale uint32
	var duration uint64
	if version == 1 {
		if len(payload) < 32 {
			return 0
		}
		timescale = binary.BigEndian.Uint32(payload[20:24])
		duration = binary.BigEndian.Uint64(payload[24:32])
	} else {
		timescale = binary.BigEndian.Uint32(payload[12:16])
		duration = uint64(binary.BigEndian.Uint32(payload[16:20]))
	}
	if timescale == 0 {
		return 0
	}
	return int64(duration) * 10_000_000 / int64(timescale)
}

// DurationBytes is a helper for embedded media.
func DurationBytes(b []byte) int64 {
	return MP4Duration(bytes.NewReader(b))
}

type box struct {
	start, end int64
	kind       string
}

func findBox(r io.ReadSeeker, start, end int64, kind string, depth int) (box, bool) {
	if depth > 6 {
		return box{}, false
	}
	pos := start
	for pos+8 <= end {
		if _, err := r.Seek(pos, io.SeekStart); err != nil {
			return box{}, false
		}
		hdr := make([]byte, 8)
		if _, err := io.ReadFull(r, hdr); err != nil {
			return box{}, false
		}
		sz := int64(binary.BigEndian.Uint32(hdr[0:4]))
		typ := string(hdr[4:8])
		headerLen := int64(8)
		if sz == 1 {
			ext := make([]byte, 8)
			if _, err := io.ReadFull(r, ext); err != nil {
				return box{}, false
			}
			sz = int64(binary.BigEndian.Uint64(ext))
			headerLen = 16
		} else if sz == 0 {
			sz = end - pos
		}
		if sz < headerLen || pos+sz > end {
			return box{}, false
		}
		if typ == kind {
			return box{start: pos + headerLen, end: pos + sz, kind: typ}, true
		}
		if typ == "moov" || typ == "trak" || typ == "mdia" {
			if inner, ok := findBox(r, pos+headerLen, pos+sz, kind, depth+1); ok {
				return inner, true
			}
		}
		pos += sz
	}
	return box{}, false
}

// ExtractEmbeddedSubtitle asks ffmpeg to convert one subtitle stream to WebVTT.
func ExtractEmbeddedSubtitle(ctx context.Context, ffmpeg, path string, streamIndex int) ([]byte, error) {
	if ffmpeg == "" {
		return nil, fmt.Errorf("ffmpeg not available")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-i", path,
		"-map", "0:"+strconv.Itoa(streamIndex), "-f", "webvtt", "pipe:1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg subtitle extract: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty subtitle stream")
	}
	return out, nil
}

// normalizeMedia rewrites stream indexes into the compact scheme Nuvio exposes:
// video 0, audio 1..N, subtitles afterwards.
func normalizeMedia(info *model.MediaInfo, external []model.Subtitle) {
	for i := range info.AudioTracks {
		info.AudioTracks[i].Index = i + 1
		if i == 0 {
			info.AudioTracks[i].Default = true
		}
	}
	if len(info.AudioTracks) == 0 {
		codec := info.AudioCodec
		if codec == "" {
			codec = "aac"
		}
		ch := info.AudioChannels
		if ch == 0 {
			ch = 2
		}
		info.AudioTracks = []model.AudioTrack{{Index: 1, Codec: codec, Channels: ch, Default: true, Language: "und"}}
	}
	if info.AudioCodec == "" {
		info.AudioCodec = info.AudioTracks[0].Codec
	}
	if info.AudioChannels == 0 {
		info.AudioChannels = info.AudioTracks[0].Channels
	}
	next := len(info.AudioTracks) + 1
	subs := make([]model.Subtitle, 0, len(external)+len(info.Subtitles))
	for _, s := range external {
		s.Index = next
		next++
		subs = append(subs, s)
	}
	for _, s := range info.Subtitles {
		s.Index = next
		next++
		subs = append(subs, s)
	}
	info.Subtitles = subs
}
