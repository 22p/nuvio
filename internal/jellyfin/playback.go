package jellyfin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"nuvio/internal/demo"
	"nuvio/internal/hls"
	"nuvio/internal/media"
	"nuvio/internal/model"
)

type directPlayProfile struct {
	Container  string `json:"Container"`
	VideoCodec string `json:"VideoCodec"`
	AudioCodec string `json:"AudioCodec"`
	Type       string `json:"Type"`
}

type transcodingProfile struct {
	Container        string `json:"Container"`
	Type             string `json:"Type"`
	VideoCodec       string `json:"VideoCodec"`
	AudioCodec       string `json:"AudioCodec"`
	Protocol         string `json:"Protocol"`
	Context          string `json:"Context"`
	MaxAudioChannels string `json:"MaxAudioChannels"`
}

type deviceProfile struct {
	MaxStreamingBitrate int64                `json:"MaxStreamingBitrate"`
	DirectPlayProfiles  []directPlayProfile  `json:"DirectPlayProfiles"`
	TranscodingProfiles []transcodingProfile `json:"TranscodingProfiles"`
	SubtitleProfiles    []struct {
		Format string `json:"Format"`
		Method string `json:"Method"`
	} `json:"SubtitleProfiles"`
}

type playbackRequest struct {
	UserId               string         `json:"UserId"`
	StartTimeTicks       int64          `json:"StartTimeTicks"`
	AudioStreamIndex     *int           `json:"AudioStreamIndex"`
	SubtitleStreamIndex  *int           `json:"SubtitleStreamIndex"`
	MaxStreamingBitrate  int64          `json:"MaxStreamingBitrate"`
	MediaSourceId        string         `json:"MediaSourceId"`
	DeviceProfile        *deviceProfile `json:"DeviceProfile"`
	EnableDirectPlay     *bool          `json:"EnableDirectPlay"`
	EnableDirectStream   *bool          `json:"EnableDirectStream"`
	EnableTranscoding    *bool          `json:"EnableTranscoding"`
	AllowVideoStreamCopy *bool          `json:"AllowVideoStreamCopy"`
	AllowAudioStreamCopy *bool          `json:"AllowAudioStreamCopy"`
	PlaySessionId        string         `json:"PlaySessionId"`
}

type playbackInfoResponse struct {
	MediaSources  []mediaSourceDTO `json:"MediaSources"`
	PlaySessionId string           `json:"PlaySessionId"`
	ErrorCode     string           `json:"ErrorCode,omitempty"`
}

func (a *API) registerMedia(mux *http.ServeMux) {
	mux.HandleFunc("POST /Items/{itemId}/PlaybackInfo", a.handlePlaybackInfo)
	mux.HandleFunc("GET /Items/{itemId}/PlaybackInfo", a.handlePlaybackInfo)

	for _, prefix := range []string{"/Videos", "/videos"} {
		mux.HandleFunc("GET "+prefix+"/{itemId}/stream", a.handleStream)
		mux.HandleFunc("GET "+prefix+"/{itemId}/{file}", a.handleStreamFile)
		mux.HandleFunc("GET "+prefix+"/{itemId}/master.m3u8", a.handleMaster)
		mux.HandleFunc("GET "+prefix+"/{itemId}/hls/{sessionId}/{file}", a.handleHLSFile)
		mux.HandleFunc("GET "+prefix+"/{itemId}/{mediaSourceId}/Subtitles/{index}/{file}", a.handleSubtitle)
		mux.HandleFunc("GET "+prefix+"/{itemId}/{mediaSourceId}/Subtitles/{index}/{start}/{file}", a.handleSubtitle)
		// Legacy 10.x apps request subtitles without a media source id.
		mux.HandleFunc("GET "+prefix+"/{itemId}/Subtitles/{index}/{file}", a.handleSubtitle)
	}
	mux.HandleFunc("GET /Items/{itemId}/Images/{imageType}", a.handleImage)
	mux.HandleFunc("GET /Items/{itemId}/Images/{imageType}/{imageIndex}", a.handleImage)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func (a *API) handlePlaybackInfo(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
	if !ok {
		return
	}
	it, ok := a.Catalog.Get(normalizeID(r.PathValue("itemId")))
	if !ok || !canSeeItem(u, it) {
		writeError(w, 404, "Item does not exist")
		return
	}
	req := playbackRequest{}
	if r.Method == http.MethodPost && r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req)
	}
	qp := queryValues(r)
	if req.StartTimeTicks == 0 {
		req.StartTimeTicks, _ = strconv.ParseInt(qp.Get("startTimeTicks"), 10, 64)
	}
	if req.MaxStreamingBitrate == 0 {
		req.MaxStreamingBitrate, _ = strconv.ParseInt(qp.Get("maxStreamingBitrate"), 10, 64)
	}
	if req.AudioStreamIndex == nil {
		if v := hls.ParseInt(qp.Get("audioStreamIndex"), -1); v >= 0 {
			req.AudioStreamIndex = &v
		}
	}
	if req.SubtitleStreamIndex == nil {
		if v := hls.ParseInt(qp.Get("subtitleStreamIndex"), -1); v >= 0 {
			req.SubtitleStreamIndex = &v
		}
	}
	if req.MediaSourceId == "" {
		req.MediaSourceId = qp.Get("mediaSourceId")
	}
	if req.UserId == "" {
		req.UserId = u.ID
	}
	psid := req.PlaySessionId
	if psid == "" {
		psid = randomHex(12)
	}
	if it.Media == nil {
		writeJSON(w, 200, playbackInfoResponse{
			MediaSources: []mediaSourceDTO{}, PlaySessionId: psid, ErrorCode: "NoCompatibleStream",
		})
		return
	}
	if id := normalizeID(req.MediaSourceId); id != "" && id != a.mediaSourceID(it) && id != it.ID {
		writeError(w, 404, "Media source not found")
		return
	}
	src := a.buildMediaSource(it, a.tokenFromRequest(r), &req, psid, u)
	writeJSON(w, 200, playbackInfoResponse{MediaSources: []mediaSourceDTO{src}, PlaySessionId: psid})
}

func commaHas(list, v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return true
	}
	for _, part := range strings.Split(list, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == v {
			return true
		}
	}
	return false
}

func directAllowed(it *model.Item, req *playbackRequest) bool {
	if req == nil {
		req = &playbackRequest{}
	}
	if it.Media == nil || it.Container == "" {
		return false
	}
	if req.EnableDirectPlay != nil && !*req.EnableDirectPlay {
		return false
	}
	max := req.MaxStreamingBitrate
	if max == 0 && req.DeviceProfile != nil {
		max = req.DeviceProfile.MaxStreamingBitrate
	}
	if max > 0 && it.Media.Bitrate > max*11/10 {
		return false
	}
	if req.AudioStreamIndex != nil && *req.AudioStreamIndex > 1 {
		return false
	}
	p := req.DeviceProfile
	if p == nil || len(p.DirectPlayProfiles) == 0 {
		// No client profile: let the client try the original file.
		return true
	}
	for _, dp := range p.DirectPlayProfiles {
		if dp.Type != "" && !strings.EqualFold(dp.Type, "Video") {
			continue
		}
		if !commaHas(dp.Container, it.Container) {
			continue
		}
		if dp.VideoCodec != "" && !commaHas(dp.VideoCodec, it.Media.VideoCodec) {
			continue
		}
		if dp.AudioCodec != "" && !commaHas(dp.AudioCodec, it.Media.AudioCodec) {
			continue
		}
		return true
	}
	return false
}

func (a *API) buildMediaSource(it *model.Item, token string, req *playbackRequest, psid string, u *model.User) mediaSourceDTO {
	if req == nil {
		req = &playbackRequest{}
	}
	settings := a.Store.Settings()
	msid := a.mediaSourceID(it)
	src := mediaSourceDTO{
		Protocol:              "File",
		Id:                    msid,
		Path:                  it.Path,
		Type:                  "Default",
		Container:             it.Container,
		Size:                  it.Size,
		Name:                  fileName(it.Path),
		RunTimeTicks:          it.RuntimeTicks,
		IsRemote:              false,
		ETag:                  it.ID,
		ReadAtNativeFramerate: false,
		SupportsProbing:       true,
		// Required by the Jellyfin SDK models; "http" is the default used by
		// the reference server for direct play.
		TranscodingSubProtocol:     "http",
		DefaultSubtitleStreamIndex: -1,
		Formats:                    []string{},
	}
	if src.Name == "" {
		src.Name = it.Name
	}
	if it.Media != nil {
		src.RunTimeTicks = it.Media.DurationTicks
		src.Bitrate = it.Media.Bitrate
		if src.Container == "" {
			src.Container = it.Media.Container
		}
		src.MediaStreams = a.buildStreams(it, token)
		if len(src.MediaStreams) > 0 {
			for _, s := range src.MediaStreams {
				if s.Type == "Audio" {
					src.DefaultAudioStreamIndex = s.Index
					break
				}
			}
		}
	}
	direct := directAllowed(it, req)
	ffmpeg := a.FFmpeg()
	allowTranscode := u == nil || u.AllowTranscode
	allowRemux := u == nil || u.AllowRemux
	canTranscode := ffmpeg != "" && settings.TranscodeEnabled && allowTranscode
	canRemux := ffmpeg != "" && settings.RemuxEnabled && allowRemux
	remux := false
	if !direct {
		switch {
		case canRemux && remuxEligible(it, req):
			// The streams are already client friendly; only the container
			// changes. This is much cheaper than a full encode.
			remux = true
			direct = false
		case !canTranscode:
			// Without ffmpeg or permission there is no alternative; let the
			// client try direct play.
			direct = true
		}
	}
	if req.EnableDirectPlay != nil && !*req.EnableDirectPlay && !canTranscode && !canRemux {
		direct = true
	}
	src.SupportsDirectPlay = direct
	src.SupportsDirectStream = direct
	src.SupportsTranscoding = canTranscode || canRemux

	if direct {
		src.DirectStreamUrl = fmt.Sprintf("/Videos/%s/stream?static=true&mediaSourceId=%s", it.ID, msid)
		if token != "" {
			src.DirectStreamUrl += "&api_key=" + url.QueryEscape(token)
		}
	}
	if canTranscode || remux {
		bitrate := req.MaxStreamingBitrate
		if bitrate <= 0 {
			bitrate = int64(settings.MaxBitrateMbps) * 1_000_000
		}
		if bitrate <= 0 {
			bitrate = 8_000_000
		}
		audio := 1
		if req.AudioStreamIndex != nil && *req.AudioStreamIndex > 0 {
			audio = *req.AudioStreamIndex
		}
		height := 1080
		width := 1920
		if it.Media != nil {
			if it.Media.Height > 0 && it.Media.Height < height {
				height = it.Media.Height
			}
			if it.Media.Width > 0 {
				width = it.Media.Width * height / maxInt(it.Media.Height, 1)
			}
		}
		q := url.Values{}
		q.Set("MediaSourceId", msid)
		q.Set("PlaySessionId", psid)
		codec := TranscodeCodec(settings.TranscodeCodec)
		q.Set("VideoCodec", codec)
		q.Set("AudioCodec", "aac")
		q.Set("AudioStreamIndex", strconv.Itoa(audio))
		q.Set("MaxStreamingBitrate", strconv.FormatInt(bitrate, 10))
		q.Set("Height", strconv.Itoa(height))
		q.Set("Width", strconv.Itoa(width))
		if req.StartTimeTicks > 0 {
			q.Set("StartTimeTicks", strconv.FormatInt(req.StartTimeTicks, 10))
		}
		if remux {
			q.Set("Remux", "1")
		}
		if token != "" {
			q.Set("api_key", token)
		}
		src.TranscodingUrl = "/Videos/" + it.ID + "/master.m3u8?" + q.Encode()
		src.TranscodingSubProtocol = "hls"
		src.TranscodingContainer = "ts"
		if !remux && codec != "h264" {
			src.TranscodingContainer = "mp4"
		}
	}
	return src
}

// remuxEligible reports whether the file can be repackaged into HLS without
// re-encoding video or audio. Only browser friendly codecs qualify; anything
// else has to be transcoded.
func remuxEligible(it *model.Item, req *playbackRequest) bool {
	if it == nil || it.Media == nil {
		return false
	}
	if req != nil && req.EnableDirectStream != nil && !*req.EnableDirectStream {
		return false
	}
	video := strings.ToLower(strings.TrimSpace(it.Media.VideoCodec))
	if video != "h264" && video != "avc1" && video != "avc" {
		return false
	}
	audio := strings.ToLower(strings.TrimSpace(it.Media.AudioCodec))
	if req != nil && req.AudioStreamIndex != nil && *req.AudioStreamIndex > 0 {
		for _, track := range it.Media.AudioTracks {
			if track.Index == *req.AudioStreamIndex {
				audio = strings.ToLower(strings.TrimSpace(track.Codec))
				break
			}
		}
	}
	if audio != "aac" && audio != "mp3" {
		return false
	}
	max := int64(0)
	if req != nil {
		max = req.MaxStreamingBitrate
		if max == 0 && req.DeviceProfile != nil {
			max = req.DeviceProfile.MaxStreamingBitrate
		}
	}
	if max > 0 && it.Media.Bitrate > max*11/10 {
		return false
	}
	return true
}

func (a *API) buildStreams(it *model.Item, token string) []mediaStreamDTO {
	if it.Media == nil {
		return nil
	}
	m := it.Media
	out := make([]mediaStreamDTO, 0, len(m.AudioTracks)+len(m.Subtitles)+1)
	videoTitle := strings.ToUpper(m.VideoCodec)
	if m.Height >= 2160 {
		videoTitle = "4K " + videoTitle
	} else if m.Height >= 1080 {
		videoTitle = "1080p " + videoTitle
	} else if m.Height >= 720 {
		videoTitle = "720p " + videoTitle
	} else if m.Height > 0 {
		videoTitle = fmt.Sprintf("%dp %s", m.Height, videoTitle)
	}
	out = append(out, mediaStreamDTO{
		Type: "Video", Index: 0, Codec: m.VideoCodec, DisplayTitle: strings.TrimSpace(videoTitle),
		IsDefault: true, Width: m.Width, Height: m.Height, BitRate: m.Bitrate,
		Profile: m.VideoProfile, RealFrameRate: m.FrameRate, AverageFrameRate: m.FrameRate,
		VideoRange: "SDR", IsInterlaced: false,
	})
	for _, at := range m.AudioTracks {
		layout := "stereo"
		if at.Channels > 2 {
			layout = fmt.Sprintf("%d.1", at.Channels-1)
		}
		title := fmt.Sprintf("%s - %s - %s", media.LanguageName(at.Language), strings.ToUpper(at.Codec), layout)
		out = append(out, mediaStreamDTO{
			Type: "Audio", Index: at.Index, Codec: at.Codec, Language: at.Language,
			DisplayTitle: title, DisplayLanguage: media.LanguageName(at.Language),
			Channels: at.Channels, ChannelLayout: layout, IsDefault: at.Default,
		})
	}
	for _, s := range m.Subtitles {
		title := s.Title
		if title == "" {
			title = media.LanguageName(s.Language)
		}
		// Report the format the delivery URL actually returns. External ASS
		// files are served untouched so clients with a real ASS renderer keep
		// their typesetting; everything else is converted on demand.
		codec, format := subtitleDelivery(s)
		out = append(out, mediaStreamDTO{
			Type: "Subtitle", Index: s.Index, Codec: codec, Language: s.Language,
			Title: title, DisplayTitle: title, DisplayLanguage: media.LanguageName(s.Language),
			IsExternal: true, IsForced: s.Forced, IsDefault: s.Default,
			DeliveryMethod: "External", DeliveryUrl: a.subtitleURL(it, s.Index, format, token),
			SupportsExternalStream: true, IsTextSubtitleStream: true,
		})
	}
	return out
}

// subtitleDelivery returns the Jellyfin codec name and the file extension the
// subtitle endpoint serves for a stream. External ASS/SSA sidecars keep their
// native format so effects and positioning survive; embedded ASS has to be
// converted (the extraction pipeline produces WebVTT) and is delivered as
// SubRip.
func subtitleDelivery(s model.Subtitle) (codec, format string) {
	switch strings.ToLower(strings.TrimSpace(s.Codec)) {
	case "ass", "ssa":
		if s.Path != "" {
			return "ass", "ass"
		}
	case "vtt", "webvtt":
		return "webvtt", "vtt"
	}
	return "subrip", "srt"
}

// Media access ------------------------------------------------------------

type sizeReadSeeker struct {
	*bytes.Reader
}

func (sizeReadSeeker) Close() error { return nil }

func (a *API) openMedia(it *model.Item) (io.ReadSeekCloser, int64, time.Time, string, error) {
	if it.Demo {
		b := demo.SampleBytes()
		return sizeReadSeeker{bytes.NewReader(b)}, int64(len(b)), it.AddedAt, "sample.mp4", nil
	}
	f, err := os.Open(it.Path)
	if err != nil {
		return nil, 0, time.Time{}, "", err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, time.Time{}, "", err
	}
	return f, st.Size(), st.ModTime(), filepath.Base(it.Path), nil
}

func contentTypeFor(container string) string {
	switch strings.ToLower(container) {
	case "mp4":
		return "video/mp4"
	case "webm":
		return "video/webm"
	case "mkv":
		return "video/x-matroska"
	case "ts":
		return "video/mp2t"
	case "avi":
		return "video/x-msvideo"
	case "mov":
		return "video/quicktime"
	}
	return "application/octet-stream"
}

func (a *API) handleStreamFile(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	if !strings.HasPrefix(file, "stream.") {
		http.NotFound(w, r)
		return
	}
	a.handleStream(w, r)
}

// streamUser resolves the caller for direct streaming. Jellyfin serves
// /Videos/{itemId}/stream without authentication (clients such as Findroid
// open the URL with no credentials), so a missing token is allowed while a
// supplied token is still validated.
func (a *API) streamUser(r *http.Request) (*model.User, bool) {
	if a.tokenFromRequest(r) == "" {
		return nil, true
	}
	return a.userFromRequest(r)
}

func (a *API) handleStream(w http.ResponseWriter, r *http.Request) {
	u, ok := a.streamUser(r)
	if !ok {
		writeError(w, 401, "Not signed in")
		return
	}
	it, ok := a.Catalog.Get(normalizeID(r.PathValue("itemId")))
	if !ok || (u != nil && !canSeeItem(u, it)) {
		writeError(w, 404, "Item does not exist")
		return
	}
	rs, _, mod, name, err := a.openMedia(it)
	if err != nil {
		writeError(w, 404, "Could not read the media file")
		return
	}
	defer rs.Close()
	w.Header().Set("Content-Type", contentTypeFor(it.Container))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	http.ServeContent(w, r, name, mod, rs)
}

// transcodeTarget resolves the hardware backend, render device, output codec
// and software encoder for a session. A hardware backend is only used when it
// can actually encode the selected codec; otherwise the codec's software
// encoder is used, and finally H.264 when no software encoder exists either.
func (a *API) transcodeTarget(mode, codec string) (string, string, string, string) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == "auto" {
		mode = a.HardwareMode()
	}
	device := a.HardwareDevice(mode)
	if device == "" {
		mode = ""
	}
	codec = TranscodeCodec(codec)
	if mode != "" && !a.SupportsEncode(mode, codec) {
		mode, device = "", ""
	}
	encoder := a.SoftwareEncoder(codec)
	if mode == "" && encoder == "" && codec != "h264" {
		codec = "h264"
		encoder = a.SoftwareEncoder("h264")
		if encoder == "" {
			encoder = a.Encoder()
		}
	}
	if mode == "" && codec == "h264" && encoder == "" {
		encoder = a.Encoder()
	}
	return mode, device, codec, encoder
}

func (a *API) handleMaster(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	it, ok := a.Catalog.Get(normalizeID(r.PathValue("itemId")))
	if !ok || !canSeeItem(u, it) {
		writeError(w, 404, "Item does not exist")
		return
	}
	if it.Media == nil {
		writeError(w, 404, "The item has no media source")
		return
	}
	settings := a.Store.Settings()
	q := queryValues(r)
	remux := q.Get("Remux") == "1"
	if remux {
		if !settings.RemuxEnabled {
			writeError(w, 403, "Remuxing is disabled on this server")
			return
		}
		if !u.AllowRemux {
			writeError(w, 403, "This account may not remux")
			return
		}
	} else {
		if !settings.TranscodeEnabled {
			writeError(w, 403, "Transcoding is disabled on this server")
			return
		}
		if !u.AllowTranscode {
			writeError(w, 403, "This account may not transcode")
			return
		}
	}
	ffmpeg := a.FFmpeg()
	if ffmpeg == "" {
		writeError(w, 503, "ffmpeg is not configured on this server")
		return
	}
	psid := q.Get("PlaySessionId")
	if psid == "" {
		psid = randomHex(12)
	}
	mode, device, codec, encoder := a.transcodeTarget(settings.HardwareAccel, settings.TranscodeCodec)
	opts := hls.Options{
		BitrateKbps:    int(hls.ParseInt(q.Get("MaxStreamingBitrate"), 8_000_000) / 1000),
		AudioIndex:     hls.ParseInt(q.Get("AudioStreamIndex"), 1),
		StartTicks:     int64(hls.ParseInt(q.Get("StartTimeTicks"), 0)),
		Height:         hls.ParseInt(q.Get("Height"), 720),
		Width:          hls.ParseInt(q.Get("Width"), 0),
		Preset:         settings.TranscodePreset,
		Hardware:       mode,
		HardwareDevice: device,
		VideoCodec:     it.Media.VideoCodec,
		Encoder:        encoder,
		Codec:          codec,
		UserID:         u.ID,
		Copy:           remux,
	}
	if remux && it.Media != nil {
		// Repackaging keeps the original resolution and bitrate.
		if it.Media.Height > 0 {
			opts.Height = it.Media.Height
		}
		if it.Media.Width > 0 {
			opts.Width = it.Media.Width
		}
		if it.Media.Bitrate > 0 {
			opts.BitrateKbps = int(it.Media.Bitrate / 1000)
		}
	}
	mediaPath := it.Path
	if it.Demo {
		mediaPath = a.DemoFile
	}
	if mediaPath == "" {
		writeError(w, 500, "Demo media is not initialized")
		return
	}
	sess, err := a.HLS.Ensure(psid, it.ID, mediaPath, opts)
	if err != nil {
		writeError(w, 500, "Could not start transcoding: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, sess.Master(q.Get("api_key")))
}

func (a *API) handleHLSFile(w http.ResponseWriter, r *http.Request) {
	itemID := normalizeID(r.PathValue("itemId"))
	sid := r.PathValue("sessionId")
	file := r.PathValue("file")
	sess, ok := a.HLS.Get(sid)
	if !ok || sess.ItemID != itemID {
		http.Error(w, "Transcoding session not found", http.StatusNotFound)
		return
	}
	if file == "main.m3u8" {
		// The janitor suspends sessions that stopped receiving requests, so
		// resume before waiting for output.
		if sess, ok = a.HLS.Resume(sid, -1); !ok {
			http.Error(w, "Transcoding session not found", http.StatusNotFound)
			return
		}
		sess.Touch()
		path, ok := sess.WaitForPlaylist(25 * time.Second)
		if !ok {
			if next, retried := a.HLS.FallbackSoftware(sid); retried {
				sess = next
				path, ok = sess.WaitForPlaylist(25 * time.Second)
			}
		}
		if !ok {
			msg := sess.Err()
			if msg == "" {
				msg = "Timed out waiting for transcoding output"
			}
			http.Error(w, msg, http.StatusBadGateway)
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			http.Error(w, "Could not read the playlist", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
		return
	}
	if index := hls.SegmentIndex(file); index >= 0 {
		// A paused client may resume straight into a segment request without
		// re-reading the playlist first.
		if sess, ok = a.HLS.Resume(sid, index); !ok {
			http.Error(w, "Transcoding session not found", http.StatusNotFound)
			return
		}
		sess.TouchSegment(index)
		path, ok := sess.WaitForSegment(file, 20*time.Second)
		if !ok {
			http.Error(w, "Segment not found", http.StatusNotFound)
			return
		}
		serveSessionFile(w, r, file, path)
		return
	}
	sess.Touch()
	path, ok := sess.SegmentPath(file)
	if !ok {
		http.Error(w, "Segment not found", http.StatusNotFound)
		return
	}
	serveSessionFile(w, r, file, path)
}

func serveSessionFile(w http.ResponseWriter, r *http.Request, name, path string) {
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "Segment not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	ct := "video/mp2t"
	switch {
	case strings.HasSuffix(name, ".mp4"), strings.HasSuffix(name, ".m4s"):
		ct = "video/mp4"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=600")
	http.ServeContent(w, r, name, st.ModTime(), f)
}

func (a *API) handleSubtitle(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	it, ok := a.Catalog.Get(normalizeID(r.PathValue("itemId")))
	if !ok || it.Media == nil || !canSeeItem(u, it) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	idx := hls.ParseInt(r.PathValue("index"), -1)
	var sub *model.Subtitle
	for i := range it.Media.Subtitles {
		if it.Media.Subtitles[i].Index == idx {
			sub = &it.Media.Subtitles[i]
			break
		}
	}
	if sub == nil {
		http.Error(w, "subtitle not found", http.StatusNotFound)
		return
	}
	format := strings.ToLower(r.PathValue("file"))
	format = strings.TrimPrefix(format, "stream.")

	// Serve external ASS/SSA files untouched so capable players (MPV, libass)
	// keep the original typesetting instead of a flattened SubRip conversion.
	if format == "ass" || format == "ssa" {
		if sub.Path == "" || sub.Embedded {
			http.Error(w, "unsupported subtitle format", http.StatusNotFound)
			return
		}
		data, err := os.ReadFile(sub.Path)
		if err != nil {
			http.Error(w, "subtitle unavailable", http.StatusNotFound)
			return
		}
		data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
		contentType := "text/x-ssa"
		if utf8.Valid(data) {
			contentType += "; charset=utf-8"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "public, max-age=600")
		_, _ = w.Write(data)
		return
	}

	var data []byte
	var err error
	switch {
	case strings.HasPrefix(sub.Path, "demo://"):
		data, _ = demo.SubtitleBytes(sub.Path)
		if data == nil {
			err = fmt.Errorf("demo subtitle missing")
		}
	case sub.Path != "":
		data, err = media.SubtitleToVTT(sub.Path)
	case sub.Embedded:
		data, err = a.extractSubtitle(r, it, *sub)
	default:
		err = fmt.Errorf("subtitle has no source")
	}
	if err != nil {
		http.Error(w, "subtitle unavailable", http.StatusNotFound)
		return
	}
	contentType := "text/vtt; charset=utf-8"
	switch format {
	case "vtt", "webvtt", "":
	case "srt", "subrip":
		data = vttToSRT(data)
		contentType = "application/x-subrip; charset=utf-8"
	default:
		http.Error(w, "unsupported subtitle format", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=600")
	_, _ = w.Write(data)
}

// vttToSRT converts the WebVTT produced by the subtitle pipeline into SubRip.
func vttToSRT(data []byte) []byte {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.HasPrefix(text, "WEBVTT") {
		if i := strings.Index(text, "\n\n"); i >= 0 {
			text = text[i+2:]
		} else {
			text = ""
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var b strings.Builder
	n := 0
	for _, block := range strings.Split(text, "\n\n") {
		lines := strings.Split(block, "\n")
		idx := -1
		for i, line := range lines {
			if strings.Contains(line, "-->") {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		n++
		fmt.Fprintf(&b, "%d\n%s\n", n, strings.ReplaceAll(lines[idx], ".", ","))
		for _, line := range lines[idx+1:] {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func (a *API) extractSubtitle(r *http.Request, it *model.Item, sub model.Subtitle) ([]byte, error) {
	if a.FFmpeg() == "" {
		return nil, fmt.Errorf("ffmpeg unavailable")
	}
	dir := filepath.Join(a.DataDir, "subtitles")
	_ = os.MkdirAll(dir, 0o755)
	cache := filepath.Join(dir, fmt.Sprintf("%s-%d.vtt", it.ID, sub.Index))
	if b, err := os.ReadFile(cache); err == nil && len(b) > 0 {
		return b, nil
	}
	path := it.Path
	if it.Demo {
		path = a.DemoFile
	}
	data, err := media.ExtractEmbeddedSubtitle(r.Context(), a.FFmpeg(), path, sub.StreamIndex)
	if err != nil {
		return nil, err
	}
	_ = os.WriteFile(cache, data, 0o644)
	return data, nil
}
