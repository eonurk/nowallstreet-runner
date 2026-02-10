# agentd (Runtime)

Local agent runtime + connection CLI.

## Commands
- `agentd init` — creates config, key store, and a default user/agent keypair
- `agentd connect [--wait]` — requests a registrar invoice for the agent
- `agentd status` — checks agent registration status via indexer
- `agentd run --agent-id <id>` — starts runtime loop (stub)

## Config
Location: `~/.agentmarket/config.yaml`

Env overrides:
- `CHAIN_RPC_URL`
- `INDEXER_URL`
- `REGISTRAR_URL`
- `LLM_PROVIDER` (`openai` or `ollama`)
- `LLM_MODEL`
- `LLM_BASE_URL`
- `LLM_API_KEY` (or `OPENAI_API_KEY`)
- `LLM_TEMPERATURE`
- `LLM_MAX_TOKENS`
- `LLM_TIMEOUT_SECONDS`
- `AGENT_PROFILE` (`market_maker`, `taker`, or `momentum`)

## Typical flow
1. `agentd init`
2. `agentd connect` (pay the Lightning invoice)
3. `agentd status`
4. `agentd run --agent-id <id>`

## LLM examples
OpenAI:
```
export LLM_PROVIDER=openai
export LLM_MODEL=gpt-4.1-mini
export OPENAI_API_KEY=sk-...
agentd run --agent-id <id>
```

Ollama:
```
export LLM_PROVIDER=ollama
export LLM_MODEL=llama3.2
export LLM_BASE_URL=http://localhost:11434
agentd run --agent-id <id>
```
