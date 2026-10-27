package jellyfin

import (
	"testing"

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
	multiAudio := &model.Item{
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
	cases := []struct {
		name string
		item *model.Item
		req  *playbackRequest
		want bool
	}{
		{"h264 aac", mediaItem("h264", "aac", 5_000_000), &playbackRequest{}, true},
		{"avc1 mp3", mediaItem("avc1", "mp3", 0), &playbackRequest{}, true},
		{"hevc must transcode", mediaItem("hevc", "aac", 0), &playbackRequest{}, false},
		{"ac3 audio must transcode", mediaItem("h264", "ac3", 0), &playbackRequest{}, false},
		{"selected aac can remux", multiAudio, &playbackRequest{AudioStreamIndex: intPtr(1)}, true},
		{"selected ac3 must transcode", multiAudio, &playbackRequest{AudioStreamIndex: intPtr(2)}, false},
		{"bitrate cap must transcode", mediaItem("h264", "aac", 20_000_000), &playbackRequest{MaxStreamingBitrate: 5_000_000}, false},
		{"direct stream disabled", mediaItem("h264", "aac", 0), &playbackRequest{EnableDirectStream: boolPtr(false)}, false},
	}
	for _, tc := range cases {
		if got := remuxEligible(tc.item, tc.req); got != tc.want {
			t.Errorf("%s: remuxEligible = %v, want %v", tc.name, got, tc.want)
		}
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
