package jellyfin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nuvio/internal/demo"
	"nuvio/internal/model"
)

func mediaItem(video, audio string, bitrate int64) *model.Item {
	return &model.Item{
		Type: model.TypeMovie,
		Media: &model.MediaInfo{
			VideoCodec: video,
			AudioCodec: audio,
			Bitrate:    bitrate,
		},
	}
}

func TestRemuxEligible(t *testing.T) {
	// A browser that only decodes H.264/AAC, like a conservative web profile.
	basic := &deviceProfile{DirectPlayProfiles: []directPlayProfile{
		{Container: "mp4", Type: "Video", VideoCodec: "h264", AudioCodec: "aac"},
	}}
	// A client that declares every codec the container can carry.
	wide := &deviceProfile{DirectPlayProfiles: []directPlayProfile{
		{Container: "mp4", Type: "Video", VideoCodec: "h264,hevc,av1,vp9", AudioCodec: "aac,ac3,eac3,flac,opus"},
	}}

	cases := []struct {
		name       string
		item       *model.Item
		req        *playbackRequest
		ok         bool
		audioCopy  bool
		audioCodec string
	}{
		{"h264 aac copies both", mediaItem("h264", "aac", 0), &playbackRequest{DeviceProfile: basic}, true, true, "aac"},
		{"hevc needs client support", mediaItem("hevc", "aac", 0), &playbackRequest{DeviceProfile: basic}, false, false, ""},
		{"hevc copies when supported", mediaItem("hevc", "aac", 0), &playbackRequest{DeviceProfile: wide}, true, true, "aac"},
		{"av1 copies when supported", mediaItem("av1", "aac", 0), &playbackRequest{DeviceProfile: wide}, true, true, "aac"},
		{"vp9 copies when supported", mediaItem("vp9", "aac", 0), &playbackRequest{DeviceProfile: wide}, true, true, "aac"},
		{"flac audio copies when supported", mediaItem("h264", "flac", 0), &playbackRequest{DeviceProfile: wide}, true, true, "flac"},
		{"flac audio re-encoded otherwise", mediaItem("h264", "flac", 0), &playbackRequest{DeviceProfile: basic}, true, false, "aac"},
		{"ac3 audio copies when supported", mediaItem("h264", "ac3", 0), &playbackRequest{DeviceProfile: wide}, true, true, "ac3"},
		{"mp3 is never copied", mediaItem("h264", "mp3", 0), &playbackRequest{DeviceProfile: wide}, true, false, "aac"},
		{"vorbis is never copied", mediaItem("h264", "vorbis", 0), &playbackRequest{DeviceProfile: wide}, true, false, "aac"},
		{"mpeg2 video must transcode", mediaItem("mpeg2", "aac", 0), &playbackRequest{DeviceProfile: wide}, false, false, ""},
		{"no profile assumes h264", mediaItem("h264", "aac", 0), &playbackRequest{}, true, true, "aac"},
		{"no profile rejects hevc", mediaItem("hevc", "aac", 0), &playbackRequest{}, false, false, ""},
		{"bitrate cap must transcode", mediaItem("h264", "aac", 20_000_000), &playbackRequest{DeviceProfile: wide, MaxStreamingBitrate: 5_000_000}, false, false, ""},
		{"direct stream disabled", mediaItem("h264", "aac", 0), &playbackRequest{DeviceProfile: wide, EnableDirectStream: boolPtr(false)}, false, false, ""},
	}
	for _, tc := range cases {
		plan, ok := planRemux(tc.item, tc.req)
		if ok != tc.ok {
			t.Errorf("%s: planRemux = %v, want %v", tc.name, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if plan.AudioCopy != tc.audioCopy || plan.AudioCodec != tc.audioCodec {
			t.Errorf("%s: audio = %v/%q, want %v/%q", tc.name, plan.AudioCopy, plan.AudioCodec, tc.audioCopy, tc.audioCodec)
		}
	}
}

func TestPlanRemuxSelectsAudioTrack(t *testing.T) {
	item := &model.Item{
		Type: model.TypeMovie,
		Media: &model.MediaInfo{
			VideoCodec: "h264",
			AudioCodec: "aac",
			AudioTracks: []model.AudioTrack{
				{Index: 1, Codec: "aac"},
				{Index: 2, Codec: "ac3"},
			},
		},
	}
	profile := &deviceProfile{DirectPlayProfiles: []directPlayProfile{
		{Container: "mp4", Type: "Video", VideoCodec: "h264", AudioCodec: "aac,ac3"},
	}}

	plan, ok := planRemux(item, &playbackRequest{DeviceProfile: profile, AudioStreamIndex: intPtr(2)})
	if !ok || !plan.AudioCopy || plan.AudioCodec != "ac3" {
		t.Fatalf("selected track: %v %v %q", ok, plan.AudioCopy, plan.AudioCodec)
	}

	withoutAC3 := &deviceProfile{DirectPlayProfiles: []directPlayProfile{
		{Container: "mp4", Type: "Video", VideoCodec: "h264", AudioCodec: "aac"},
	}}
	plan, ok = planRemux(item, &playbackRequest{DeviceProfile: withoutAC3, AudioStreamIndex: intPtr(2)})
	if !ok || plan.AudioCopy || plan.AudioCodec != "aac" {
		t.Fatalf("undecodable track should be re-encoded: %v %v %q", ok, plan.AudioCopy, plan.AudioCodec)
	}
}

func TestLibraryVisibility(t *testing.T) {
	item := &model.Item{LibraryID: "lib-a"}
	restricted := &model.User{AllowedLibraries: []string{"lib-b"}}
	if canSeeItem(restricted, item) {
		t.Fatal("restricted user should not see lib-a")
	}
	restricted.AllowedLibraries = []string{"lib-a"}
	if !canSeeItem(restricted, item) {
		t.Fatal("explicitly allowed library was hidden")
	}
	if !canSeeItem(&model.User{}, item) {
		t.Fatal("empty policy should allow every library")
	}
	if !canSeeItem(&model.User{IsAdmin: true, AllowedLibraries: []string{"lib-b"}}, item) {
		t.Fatal("administrators always see every library")
	}
}

func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }

func TestPlaybackInfoRejectsTraversalSessionID(t *testing.T) {
	a, h := newTestAPI(t)
	_, tok := testUser(t, a)

	movieID := ""
	for _, it := range demo.Items() {
		if it.Type == model.TypeMovie {
			movieID = it.ID
			break
		}
	}
	if movieID == "" {
		t.Fatal("demo movie missing")
	}

	req := httptest.NewRequest("POST", "/Items/"+movieID+"/PlaybackInfo?api_key="+tok,
		strings.NewReader(`{"PlaySessionId":"../../escape"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestVTTToSRTStripsCueSettings(t *testing.T) {
	in := "WEBVTT\n\n00:00:01.000 --> 00:00:04.000 line:9.3%,start position:50% align:center\nHello\n"
	out := string(vttToSRT([]byte(in)))
	if !strings.Contains(out, "00:00:01,000 --> 00:00:04,000") {
		t.Fatalf("timestamps were not converted:\n%s", out)
	}
	if strings.Contains(out, "line:") || strings.Contains(out, "position:") || strings.Contains(out, "align:") {
		t.Fatalf("cue settings leaked into SRT:\n%s", out)
	}
	if !strings.Contains(out, "Hello") {
		t.Fatalf("cue text missing:\n%s", out)
	}
}
