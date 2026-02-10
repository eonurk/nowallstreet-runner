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

type openAIClient struct {
	baseURL         string
	apiKey          string
	model           string
	temperature     float64
	maxOutputTokens int
	timeout         time.Duration
}

type openAIResponse struct {
	OutputText string `json:"output_text"`
	Output     []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *openAIClient) Provider() string {
	return "openai"
}

func (c *openAIClient) Model() string {
	return c.model
}

func (c *openAIClient) Generate(ctx context.Context, prompt Prompt) (string, error) {
	payload := map[string]any{
		"model": c.model,
	}

	input := []map[string]any{}
	if strings.TrimSpace(prompt.System) != "" {
		input = append(input, map[string]any{
			"role": "system",
			"content": []map[string]any{{
				"type": "input_text",
				"text": prompt.System,
			}},
		})
	}
	if strings.TrimSpace(prompt.User) != "" {
		input = append(input, map[string]any{
			"role": "user",
			"content": []map[string]any{{
				"type": "input_text",
				"text": prompt.User,
			}},
		})
	}
	if len(input) == 0 {
		return "", fmt.Errorf("empty prompt")
	}
	payload["input"] = input
	if c.temperature > 0 {
		payload["temperature"] = c.temperature
	}
	if c.maxOutputTokens > 0 {
		payload["max_output_tokens"] = c.maxOutputTokens
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

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
		return "", fmt.Errorf("openai error (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var parsed openAIResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
		return "", fmt.Errorf("openai error: %s", parsed.Error.Message)
	}

	text := strings.TrimSpace(parsed.OutputText)
	if text != "" {
		return text, nil
	}

	var sb strings.Builder
	for _, item := range parsed.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type != "output_text" {
				continue
			}
			if strings.TrimSpace(content.Text) == "" {
				continue
			}
			sb.WriteString(content.Text)
		}
	}
	text = strings.TrimSpace(sb.String())
	if text == "" {
		return "", fmt.Errorf("openai response had no output_text")
	}
	return text, nil
}
