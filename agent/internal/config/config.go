package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Chain struct {
		RPC     string `yaml:"rpc"`
		Indexer string `yaml:"indexer"`
	} `yaml:"chain"`
	Registrar struct {
		URL string `yaml:"url"`
	} `yaml:"registrar"`
	Agent struct {
		ID                 string   `yaml:"id"`
		KeyStore           string   `yaml:"key_store"`
		SessionTTLMinutes  int      `yaml:"session_ttl_minutes"`
		SessionMaxSpendAGC uint64   `yaml:"session_max_spend_agc"`
		AllowedMsgs        []string `yaml:"allowed_msgs"`
	} `yaml:"agent"`
	Strategy struct {
		FetchTimeoutSeconds int    `yaml:"fetch_timeout_seconds"`
		CacheDir            string `yaml:"cache_dir"`
	} `yaml:"strategy"`
	LLM struct {
		Provider        string  `yaml:"provider"`
		Model           string  `yaml:"model"`
		BaseURL         string  `yaml:"base_url"`
		APIKey          string  `yaml:"api_key"`
		Temperature     float64 `yaml:"temperature"`
		MaxOutputTokens int     `yaml:"max_output_tokens"`
		TimeoutSeconds  int     `yaml:"timeout_seconds"`
	} `yaml:"llm"`
}

func Default(home string) Config {
	cfg := Config{}
	cfg.Chain.RPC = "http://localhost:26657"
	cfg.Chain.Indexer = "http://localhost:8080"
	cfg.Registrar.URL = "http://localhost:7070"
	cfg.Agent.ID = ""
	cfg.Agent.KeyStore = filepath.Join(home, ".agentmarket", "keys")
	cfg.Agent.SessionTTLMinutes = 10
	cfg.Agent.SessionMaxSpendAGC = 50
	cfg.Agent.AllowedMsgs = []string{"MsgPostOffer", "MsgCreateRFQ"}
	cfg.Strategy.FetchTimeoutSeconds = 10
	cfg.Strategy.CacheDir = filepath.Join(home, ".agentmarket", "strategy")
	cfg.LLM.Provider = ""
	cfg.LLM.Model = ""
	cfg.LLM.BaseURL = ""
	cfg.LLM.APIKey = ""
	cfg.LLM.Temperature = 0.2
	cfg.LLM.MaxOutputTokens = 256
	cfg.LLM.TimeoutSeconds = 15
	return cfg
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Write(path string, cfg Config) error {
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
