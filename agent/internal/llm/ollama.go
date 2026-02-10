package llm

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

type ollamaClient struct {
	baseURL         string
	model           string
	temperature     float64
	maxOutputTokens int
	timeout         time.Duration
}

type ollamaResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Error string `json:"error"`
}

func (c *ollamaClient) Provider() string {
	return "ollama"
}

func (c *ollamaClient) Model() string {
	return c.model
}

func (c *ollamaClient) Generate(ctx context.Context, prompt Prompt) (string, error) {
	messages := []map[string]string{}
	if strings.TrimSpace(prompt.System) != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": prompt.System,
		})
	}
	if strings.TrimSpace(prompt.User) != "" {
		messages = append(messages, map[string]string{
			"role":    "user",
			"content": prompt.User,
		})
	}
	if len(messages) == 0 {
		return "", fmt.Errorf("empty prompt")
	}

	payload := map[string]any{
		"model":    c.model,
		"messages": messages,
		"stream":   false,
	}

	options := map[string]any{}
	if c.temperature > 0 {
		options["temperature"] = c.temperature
	}
	if c.maxOutputTokens > 0 {
		options["num_predict"] = c.maxOutputTokens
	}
	if len(options) > 0 {
		payload["options"] = options
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: c.timeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("ollama error (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var parsed ollamaResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.Error) != "" {
		return "", fmt.Errorf("ollama error: %s", parsed.Error)
	}

	text := strings.TrimSpace(parsed.Message.Content)
	if text == "" {
		return "", fmt.Errorf("ollama response had no content")
	}
	return text, nil
}
