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
	out, status, err := c.post(ctx, "/chat/completions", body)
	if err != nil {
		return "", err
	}
	// Not every compatible endpoint accepts response_format.
	if status == http.StatusBadRequest && jsonMode {
		body.ResponseFormat = nil
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
