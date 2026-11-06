package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestTranslateVTTOneRequestWithBudget(t *testing.T) {
	var body map[string]any
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewDecoder(r.Body).Decode(&body)
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		raw, _ := json.Marshal(body)
		_ = json.Unmarshal(raw, &req)
		payload := req.Messages[len(req.Messages)-1].Content
		payload = payload[strings.Index(payload, "{"):]
		var in map[string]string
		_ = json.Unmarshal([]byte(payload), &in)
		out := map[string]string{}
		for k := range in {
			out[k] = "译"
		}
		content, _ := json.Marshal(out)
		res, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": string(content)}}},
		})
		_, _ = w.Write(res)
	}))
	defer server.Close()

	client := ai.New(server.URL, "key", "test")
	var b strings.Builder
	b.WriteString("WEBVTT\n")
	for i := 0; i < 40; i++ {
		b.WriteString(fmt.Sprintf("\n00:00:%02d.000 --> 00:00:%02d.500\nline %d\n", i, i, i))
	}
	vtt := b.String()
	out, err := translateVTT(context.Background(), client, vtt, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("expected one request for the whole track, got %d", requests)
	}
	if strings.Count(out, "译") != 40 {
		t.Fatalf("translated cues = %d", strings.Count(out, "译"))
	}
	if body["max_tokens"] == nil {
		t.Fatal("max_tokens was not sent")
	}
	thinking, _ := body["thinking"].(map[string]any)
	if thinking["type"] != "disabled" {
		t.Fatalf("thinking = %v", body["thinking"])
	}
}

func TestTranslateVTTSplitsTruncatedReply(t *testing.T) {
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
		content := "{"
		if len(in) == 1 {
			content = `{"0":"译"}`
		}
		body, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": content}}},
		})
		_, _ = w.Write(body)
	}))
	defer server.Close()
	client := ai.New(server.URL, "key", "test")
	vtt := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\na\n\n00:00:03.000 --> 00:00:04.000\nb\n\n00:00:05.000 --> 00:00:06.000\nc\n\n00:00:07.000 --> 00:00:08.000\nd\n"
	out, err := translateVTT(context.Background(), client, vtt, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "译") != 4 {
		t.Fatalf("expected every cue translated after splitting:\n%s", out)
	}
}
