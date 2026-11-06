package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"nuvio/internal/ai"
)

var sentLineRe = regexp.MustCompile(`^(\d+)\|([^|]*)\|([^|]*)\|(.*)$`)

// echoTranslator replies to every cue line with the same index and timings and
// a marker in front of the text.
func echoTranslator(requests *int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if requests != nil {
			*requests++
		}
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		lines := []string{}
		for _, line := range strings.Split(req.Messages[len(req.Messages)-1].Content, "\n") {
			if m := sentLineRe.FindStringSubmatch(strings.TrimSpace(line)); len(m) == 5 {
				lines = append(lines, fmt.Sprintf("%s|%s|%s|译:%s", m[1], m[2], m[3], m[4]))
			}
		}
		body, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": strings.Join(lines, "\n")}}},
		})
		_, _ = w.Write(body)
	}
}

func TestTranslateVTTStripsEffectsAndKeepsTiming(t *testing.T) {
	server := httptest.NewServer(echoTranslator(nil))
	defer server.Close()

	client := ai.New(server.URL, "key", "test")
	vtt := "WEBVTT\n\n" +
		"00:00:01.000 --> 00:00:04.000 align:start position:0%\n<i>Hello</i> {\\an8}there\n\n" +
		"00:00:05.000 --> 00:00:08.000\nGeneral <b>Kenobi</b>\n"
	out, err := translateVTT(context.Background(), client, vtt, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "00:00:01.000 --> 00:00:04.000") {
		t.Fatalf("timing or settings wrong:\n%s", out)
	}
	if strings.Contains(out, "align:") || strings.Contains(out, "<i>") || strings.Contains(out, "\\an8") {
		t.Fatalf("effects were not stripped:\n%s", out)
	}
	if !strings.Contains(out, "译:Hello there") || !strings.Contains(out, "译:General Kenobi") {
		t.Fatalf("translated text missing:\n%s", out)
	}
}

func TestTranslateVTTOneRequestWithBudget(t *testing.T) {
	var body map[string]any
	requests := 0
	server := httptest.NewServer(echoTranslator(&requests))
	defer server.Close()

	client := ai.New(server.URL, "key", "test")
	var b strings.Builder
	b.WriteString("WEBVTT\n")
	for i := 0; i < 40; i++ {
		b.WriteString(fmt.Sprintf("\n00:00:%02d.000 --> 00:00:%02d.500\nline %d\n", i, i, i))
	}
	out, err := translateVTT(context.Background(), client, b.String(), "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("expected one request for the whole track, got %d", requests)
	}
	if strings.Count(out, "译:") != 40 {
		t.Fatalf("translated cues = %d", strings.Count(out, "译:"))
	}

	// Re-run against a handler that records the body, to check the budget.
	recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"1|0.0|1.0|译:x"}}]}`))
	}))
	defer recorder.Close()
	_, _ = translateVTT(context.Background(), ai.New(recorder.URL, "key", "test"), "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nx\n", "zh-CN")
	if body["max_tokens"] == nil {
		t.Fatal("max_tokens was not sent")
	}
	thinking, _ := body["thinking"].(map[string]any)
	if thinking["type"] != "disabled" {
		t.Fatalf("thinking = %v", body["thinking"])
	}
}

func TestTranslateVTTSplitsTruncatedReply(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		lines := []string{}
		for _, line := range strings.Split(req.Messages[len(req.Messages)-1].Content, "\n") {
			if m := sentLineRe.FindStringSubmatch(strings.TrimSpace(line)); len(m) == 5 {
				lines = append(lines, fmt.Sprintf("%s|%s|%s|译:%s", m[1], m[2], m[3], m[4]))
			}
		}
		// Simulate a response that was cut off after the first line.
		if len(lines) > 1 {
			lines = lines[:1]
		}
		body, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": strings.Join(lines, "\n")}}},
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
	if strings.Count(out, "译:") != 4 {
		t.Fatalf("expected every cue translated after splitting:\n%s", out)
	}
	if requests < 2 {
		t.Fatalf("expected a retry, got %d requests", requests)
	}
}

func TestTranslateVTTRejectsBadReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not the expected format"}}]}`))
	}))
	defer server.Close()
	client := ai.New(server.URL, "key", "test")
	_, err := translateVTT(context.Background(), client, "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHi\n", "zh-CN")
	if err == nil {
		t.Fatal("expected an error for a malformed translation")
	}
}

func TestTranslateVTTSkipsKaraokeSyllables(t *testing.T) {
	server := httptest.NewServer(echoTranslator(nil))
	defer server.Close()

	client := ai.New(server.URL, "key", "test")
	vtt := "WEBVTT\n\n" +
		"00:00:01.000 --> 00:00:01.100\n信\n\n" +
		"00:00:02.000 --> 00:00:02.100\n！\n\n" +
		"00:00:03.000 --> 00:00:03.400\n危险\n\n" +
		"00:00:04.000 --> 00:00:06.000\n这是一句台词\n"
	out, err := translateVTT(context.Background(), client, vtt, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "信") || strings.Contains(out, "！") {
		t.Fatalf("karaoke cue survived:\n%s", out)
	}
	if !strings.Contains(out, "译:危险") || !strings.Contains(out, "译:这是一句台词") {
		t.Fatalf("dialogue missing:\n%s", out)
	}
}

func replyWith(content string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": content}}},
		})
		_, _ = w.Write(body)
	}))
}

func TestTranslateVTTJoinsWrappedReplyLines(t *testing.T) {
	server := replyWith("1|1.0|4.0|译:前半\n后半")
	defer server.Close()
	client := ai.New(server.URL, "key", "test")
	out, err := translateVTT(context.Background(), client,
		"WEBVTT\n\n00:00:01.000 --> 00:00:04.000\noriginal\n", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "译:前半 后半") {
		t.Fatalf("wrapped line was not joined:\n%s", out)
	}
}

func TestTranslateVTTEscapesMarkup(t *testing.T) {
	server := replyWith("1|1.0|4.0|A & B <tag>")
	defer server.Close()
	client := ai.New(server.URL, "key", "test")
	out, err := translateVTT(context.Background(), client,
		"WEBVTT\n\n00:00:01.000 --> 00:00:04.000\noriginal\n", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "A &amp; B &lt;tag&gt;") {
		t.Fatalf("markup was not escaped:\n%s", out)
	}
}
