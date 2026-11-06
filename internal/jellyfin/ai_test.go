package jellyfin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nuvio/internal/ai"
)

func TestTranslateVTTKeepsTiming(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		payload := req.Messages[len(req.Messages)-1].Content
		payload = payload[strings.Index(payload, "{"):]
		var in map[string]string
		_ = json.Unmarshal([]byte(payload), &in)
		out := map[string]string{}
		for k, v := range in {
			out[k] = "译:" + v
		}
		body, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": string(bodyJSON(out))}}},
		})
		_, _ = w.Write(body)
	}))
	defer server.Close()

	client := ai.New(server.URL, "key", "test")
	vtt := "WEBVTT\n\n00:00:01.000 --> 00:00:04.000\nHello there.\n\n00:00:05.000 --> 00:00:08.000\nGeneral Kenobi.\n"
	out, err := translateVTT(context.Background(), client, vtt, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"00:00:01.000 --> 00:00:04.000", "00:00:05.000 --> 00:00:08.000", "译:Hello there.", "译:General Kenobi."} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func bodyJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestTranslateVTTRejectsBadReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not json"}}]}`))
	}))
	defer server.Close()
	client := ai.New(server.URL, "key", "test")
	_, err := translateVTT(context.Background(), client, "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHi\n", "zh-CN")
	if err == nil {
		t.Fatal("expected an error for a malformed translation")
	}
}
