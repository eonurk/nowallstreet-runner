package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"agentmarket/agent/internal/config"
	"agentmarket/agent/internal/indexer"
	"agentmarket/agent/internal/keys"
	"agentmarket/agent/internal/llm"
	"agentmarket/agent/internal/registrar"
	"agentmarket/agent/internal/runtime"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

func main() {
	sdkCfg := sdk.GetConfig()
	sdkCfg.SetBech32PrefixForAccount("cosmos", "cosmospub")
	sdkCfg.Seal()

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "init":
		if err := cmdInit(); err != nil {
			fmt.Fprintf(os.Stderr, "init failed: %v\n", err)
			os.Exit(1)
		}
	case "connect":
		if err := cmdConnect(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
			os.Exit(1)
		}
	case "run":
		if err := cmdRun(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "run failed: %v\n", err)
			os.Exit(1)
		}
	case "status":
		if err := cmdStatus(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "status failed: %v\n", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println("agentd init | connect | run | status")
}

func cmdInit() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	base := filepath.Join(home, ".agentmarket")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}

	cfg := config.Default(home)
	cfgPath := filepath.Join(base, "config.yaml")
	if err := os.MkdirAll(cfg.Agent.KeyStore, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Strategy.CacheDir, 0o700); err != nil {
		return err
	}

	userKeyPath := keys.DefaultUserKeyPath(cfg.Agent.KeyStore)
	agentKeyPath := keys.DefaultAgentKeyPath(cfg.Agent.KeyStore)
	userKey, userCreated, err := keys.EnsureKey(userKeyPath, "user")
	if err != nil {
		return err
	}
	agentKey, agentCreated, err := keys.EnsureKey(agentKeyPath, "agent")
	if err != nil {
		return err
	}
	cfg.Agent.ID = agentKey.Address

	if err := config.Write(cfgPath, cfg); err != nil {
		return err
	}

	fmt.Printf("initialized %s\n", cfgPath)
	fmt.Printf("user address:  %s\n", userKey.Address)
	fmt.Printf("agent address: %s\n", agentKey.Address)
	if userCreated || agentCreated {
		fmt.Printf("keys stored in %s\n", cfg.Agent.KeyStore)
	}
	return nil
}

func cmdConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "wait for payment + on-chain registration")
	poll := fs.Duration("poll", 5*time.Second, "poll interval")
	timeout := fs.Duration("timeout", 30*time.Minute, "wait timeout")
	agentID := fs.String("agent-id", "", "agent address to register")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	userKeyPath := keys.DefaultUserKeyPath(cfg.Agent.KeyStore)
	agentKeyPath := keys.DefaultAgentKeyPath(cfg.Agent.KeyStore)
	userKey, err := keys.Load(userKeyPath)
	if err != nil {
		return fmt.Errorf("user key not found, run agentd init: %w", err)
	}
	agentKey, err := keys.Load(agentKeyPath)
	if err != nil {
		return fmt.Errorf("agent key not found, run agentd init: %w", err)
	}

	selectedAgent := strings.TrimSpace(*agentID)
	if selectedAgent == "" {
		selectedAgent = strings.TrimSpace(cfg.Agent.ID)
	}
	if selectedAgent == "" {
		selectedAgent = agentKey.Address
	}

	client := registrar.New(cfg.Registrar.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	invoice, err := client.CreateInvoice(ctx, userKey.Address, selectedAgent)
	cancel()
	if err != nil {
		return err
	}

	fmt.Println("invoice created")
	fmt.Printf("  id:     %s\n", invoice.InvoiceID)
	fmt.Printf("  bolt11: %s\n", invoice.Bolt11)
	fmt.Printf("  amount: %d sats\n", invoice.AmountSats)
	fmt.Printf("  status: %s\n", invoice.Status)
	fmt.Printf("  expires: %s\n", invoice.ExpiresAt)

	if !*wait {
		fmt.Println("pay the invoice, then run: agentd status")
		return nil
	}

	deadline := time.Now().Add(*timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for payment")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		inv, err := client.GetInvoice(ctx, invoice.InvoiceID)
		cancel()
		if err != nil {
			return err
		}
		fmt.Printf("status: %s", inv.Status)
		if inv.PaidAt != "" {
			fmt.Printf(" (paid at %s)", inv.PaidAt)
		}
		fmt.Println()
		if inv.Status == "paid" && inv.ChainTxHash != "" {
			fmt.Printf("registered on-chain: %s\n", inv.ChainTxHash)
			return nil
		}
		time.Sleep(*poll)
	}
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	agentID := fs.String("agent-id", "", "agent address to run")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	selected := strings.TrimSpace(*agentID)
	if selected == "" {
		selected = strings.TrimSpace(cfg.Agent.ID)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	llmClient, err := llm.New(llm.Config{
		Provider:        cfg.LLM.Provider,
		Model:           cfg.LLM.Model,
		BaseURL:         cfg.LLM.BaseURL,
		APIKey:          cfg.LLM.APIKey,
		Temperature:     cfg.LLM.Temperature,
		MaxOutputTokens: cfg.LLM.MaxOutputTokens,
		TimeoutSeconds:  cfg.LLM.TimeoutSeconds,
	})
	if err != nil {
		return err
	}

	var idx *indexer.Client
	if cfg.Chain.Indexer != "" {
		ownerUID := strings.TrimSpace(os.Getenv("AGENT_OWNER_UID"))
		idx = indexer.New(cfg.Chain.Indexer, ownerUID)
	}

	profile := strings.TrimSpace(os.Getenv("AGENT_PROFILE"))
	userAddr := ""
	if userKey, err := keys.Load(keys.DefaultUserKeyPath(cfg.Agent.KeyStore)); err == nil {
		userAddr = strings.TrimSpace(userKey.Address)
	}
	runner := runtime.NewRunnerWithProfile(selected, userAddr, llmClient, idx, profile)
	if selected == "" {
		fmt.Println("agentd running")
	} else {
		fmt.Printf("agentd running for agent %s\n", selected)
		if llmClient != nil {
			fmt.Printf("llm provider: %s (%s)\n", llmClient.Provider(), llmClient.Model())
		}
	}
	return runner.Run(ctx)
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	agentID := fs.String("agent-id", "", "agent address to query")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	selected := strings.TrimSpace(*agentID)
	if selected == "" {
		selected = strings.TrimSpace(cfg.Agent.ID)
	}
	if selected == "" {
		return fmt.Errorf("agent id is required")
	}

	client := indexer.New(cfg.Chain.Indexer)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	agent, err := client.GetAgent(ctx, selected)
	cancel()
	if err != nil {
		return err
	}

	fmt.Println("agent status")
	fmt.Printf("  id: %s\n", agent.AgentID)
	fmt.Printf("  user: %s\n", agent.UserAddr)
	fmt.Printf("  status: %s\n", agent.Status)
	fmt.Printf("  strategy: %s (%s)\n", agent.StrategyURI, agent.StrategyVersion)
	if strings.TrimSpace(agent.StrategyPrompt) != "" {
		fmt.Printf("  strategy prompt: %s\n", agent.StrategyPrompt)
	}
	return nil
}

func loadConfig() (config.Config, error) {
	cfgPath, err := configPath()
	if err != nil {
		return config.Config{}, err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return config.Config{}, fmt.Errorf("config not found, run agentd init: %w", err)
	}
	applyEnvOverrides(&cfg)
	return cfg, nil
}

func applyEnvOverrides(cfg *config.Config) {
	if v := strings.TrimSpace(os.Getenv("CHAIN_RPC_URL")); v != "" {
		cfg.Chain.RPC = v
	}
	if v := strings.TrimSpace(os.Getenv("INDEXER_URL")); v != "" {
		cfg.Chain.Indexer = v
	}
	if v := strings.TrimSpace(os.Getenv("REGISTRAR_URL")); v != "" {
		cfg.Registrar.URL = v
	}
	if v := strings.TrimSpace(os.Getenv("LLM_PROVIDER")); v != "" {
		cfg.LLM.Provider = v
	}
	if v := strings.TrimSpace(os.Getenv("LLM_MODEL")); v != "" {
		cfg.LLM.Model = v
	}
	if v := strings.TrimSpace(os.Getenv("LLM_BASE_URL")); v != "" {
		cfg.LLM.BaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("LLM_API_KEY")); v != "" {
		cfg.LLM.APIKey = v
	}
	if v := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); v != "" && cfg.LLM.APIKey == "" {
		cfg.LLM.APIKey = v
	}
	if v := strings.TrimSpace(os.Getenv("OLLAMA_HOST")); v != "" && cfg.LLM.BaseURL == "" {
		cfg.LLM.BaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("LLM_TEMPERATURE")); v != "" {
		if value, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.LLM.Temperature = value
		}
	}
	if v := strings.TrimSpace(os.Getenv("LLM_MAX_TOKENS")); v != "" {
		if value, err := strconv.Atoi(v); err == nil {
			cfg.LLM.MaxOutputTokens = value
		}
	}
	if v := strings.TrimSpace(os.Getenv("LLM_TIMEOUT_SECONDS")); v != "" {
		if value, err := strconv.Atoi(v); err == nil {
			cfg.LLM.TimeoutSeconds = value
		}
	}
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agentmarket", "config.yaml"), nil
}
