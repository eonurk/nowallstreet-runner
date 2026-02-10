package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type Prompt struct {
	System string
	User   string
}

type Client interface {
	Generate(ctx context.Context, prompt Prompt) (string, error)
	Provider() string
	Model() string
}

type Config struct {
	Provider        string
	Model           string
	BaseURL         string
	APIKey          string
	Temperature     float64
	MaxOutputTokens int
	TimeoutSeconds  int
}

func New(cfg Config) (Client, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		return nil, nil
	}

	switch provider {
	case "openai":
		apiKey := strings.TrimSpace(cfg.APIKey)
		if apiKey == "" {
			apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
		}
		if apiKey == "" {
			return nil, errors.New("openai selected but no API key provided (OPENAI_API_KEY)")
		}
		model := strings.TrimSpace(cfg.Model)
		if model == "" {
			return nil, errors.New("openai selected but no model configured")
		}
		baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		timeout := cfg.TimeoutSeconds
		if timeout <= 0 {
			timeout = 15
		}
		return &openAIClient{
			baseURL:         baseURL,
			apiKey:          apiKey,
			model:           model,
			temperature:     cfg.Temperature,
			maxOutputTokens: cfg.MaxOutputTokens,
			timeout:         time.Duration(timeout) * time.Second,
		}, nil
	case "ollama":
		model := strings.TrimSpace(cfg.Model)
		if model == "" {
			model = "llama3.2"
		}
		baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		timeout := cfg.TimeoutSeconds
		if timeout <= 0 {
			timeout = 15
		}
		return &ollamaClient{
			baseURL:         baseURL,
			model:           model,
			temperature:     cfg.Temperature,
			maxOutputTokens: cfg.MaxOutputTokens,
			timeout:         time.Duration(timeout) * time.Second,
		}, nil
	default:
		return nil, fmt.Errorf("unknown llm provider: %s", provider)
	}
}
