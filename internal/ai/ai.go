// Package ai talks to an OpenAI-compatible chat API. It is optional: an empty
// key or base URL makes every call a no-op error the callers surface.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a minimal OpenAI-compatible client.
type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// New builds a client. The base URL must include the API prefix, for example
// https://api.deepseek.com/v1 or http://127.0.0.1:11434/v1.
func New(baseURL, apiKey, model string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:  strings.TrimSpace(apiKey),
		Model:   strings.TrimSpace(model),
		HTTP:    &http.Client{Timeout: 90 * time.Second},
	}
}

// NewEmbedding builds a client for /v1/embeddings. Batch embedding on CPU can
// take longer than a chat turn, so it gets a larger timeout.
func NewEmbedding(baseURL, apiKey, model string) *Client {
	c := New(baseURL, apiKey, model)
	c.HTTP.Timeout = 5 * time.Minute
	return c
}

// Available reports whether the client can be used.
func (c *Client) Available() bool {
	return c != nil && c.BaseURL != "" && c.APIKey != ""
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []message       `json:"messages"`
	Temperature    float64         `json:"temperature"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Thinking       *thinkingField  `json:"thinking,omitempty"`
}

// thinkingField is DeepSeek's switch. Other OpenAI-compatible servers ignore
// unknown fields, and the retry below drops it if one rejects the request.
type thinkingField struct {
	Type string `json:"type"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat sends a system and a user message and returns the reply text.
func (c *Client) Chat(ctx context.Context, system, user string, jsonMode bool) (string, error) {
	return c.ChatWith(ctx, system, user, jsonMode, 0, false)
}

// ChatWith is Chat with an output budget and an option to skip the model's
// reasoning pass, which matters for bulk tasks such as translating subtitles.
func (c *Client) ChatWith(ctx context.Context, system, user string, jsonMode bool, maxTokens int, disableThinking bool) (string, error) {
	if !c.Available() {
		return "", fmt.Errorf("AI is not configured")
	}
	if c.Model == "" {
		return "", fmt.Errorf("AI model is not selected")
	}
	body := chatRequest{
		Model:       c.Model,
		Messages:    []message{{Role: "system", Content: system}, {Role: "user", Content: user}},
		Temperature: 0.2,
	}
	if jsonMode {
		body.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	if maxTokens > 0 {
		body.MaxTokens = maxTokens
	}
	if disableThinking {
		body.Thinking = &thinkingField{Type: "disabled"}
	}
	out, status, err := c.post(ctx, "/chat/completions", body)
	if err != nil {
		return "", err
	}
	// Not every compatible endpoint accepts response_format or the DeepSeek
	// thinking switch, so retry without the optional fields once.
	if status == http.StatusBadRequest && (jsonMode || disableThinking) {
		body.ResponseFormat = nil
		body.Thinking = nil
		out, status, err = c.post(ctx, "/chat/completions", body)
		if err != nil {
			return "", err
		}
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("AI request failed: %s", snippet(out))
	}
	var res chatResponse
	if err := json.Unmarshal(out, &res); err != nil {
		return "", fmt.Errorf("AI response is not JSON: %w", err)
	}
	if res.Error != nil && res.Error.Message != "" {
		return "", fmt.Errorf("AI error: %s", res.Error.Message)
	}
	if len(res.Choices) == 0 {
		return "", fmt.Errorf("AI returned no choices")
	}
	return strings.TrimSpace(res.Choices[0].Message.Content), nil
}

// Models lists the model ids the endpoint reports.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	if !c.Available() {
		return nil, fmt.Errorf("AI is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("AI model list failed: %s", snippet(data))
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("AI model list is not JSON: %w", err)
	}
	models := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	return models, nil
}

func (c *Client) post(ctx context.Context, path string, body any) ([]byte, int, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	return data, res.StatusCode, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Embed returns one vector per input text. The API key is optional because
// local OpenAI-compatible servers usually run without authentication.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if c == nil || c.BaseURL == "" {
		return nil, fmt.Errorf("embedding server is not configured")
	}
	if c.Model == "" {
		return nil, fmt.Errorf("embedding model is not selected")
	}
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(embedRequest{Model: c.Model, Input: texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	var out embedResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("embedding reply: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != nil && out.Error.Message != "" {
			return nil, fmt.Errorf("embeddings HTTP %d: %s", resp.StatusCode, out.Error.Message)
		}
		return nil, fmt.Errorf("embeddings HTTP %d", resp.StatusCode)
	}
	vecs := make([][]float32, len(texts))
	allZero := true
	for _, d := range out.Data {
		if d.Index != 0 {
			allZero = false
			break
		}
	}
	for i, d := range out.Data {
		idx := d.Index
		// Some local servers omit index; fall back to the response order.
		if allZero && len(out.Data) == len(texts) {
			idx = i
		}
		if idx >= 0 && idx < len(vecs) {
			vecs[idx] = d.Embedding
		}
	}
	for i, v := range vecs {
		if len(v) == 0 {
			return nil, fmt.Errorf("embedding %d missing from reply", i)
		}
	}
	return vecs, nil
}
