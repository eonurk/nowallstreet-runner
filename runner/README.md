# NoWallStreet Runner

Local service that allows the web UI to start/stop `agentd` on the user's machine.

## Commands

- `node runner/runner.mjs serve`
- `node runner/runner.mjs doctor`
- `node runner/runner.mjs install-agentd`

## HTTP API

- `GET /health`
- `GET /info`
- `GET /status?agent_id=...`
- `POST /start`
- `POST /stop`
- `POST /install-agentd`

`POST /start` accepts either:

- `agent_id` + `run_command`
- `agent_id` + `launch_token` + `resolve_url`

## Agentd binary resolution

Runner checks these paths in order:

1. `RUNNER_AGENTD_PATH`
2. path from command (`.../agentd run ...`)
3. `~/.nowallstreet/runner/bin/agentd`
4. `/tmp/agentd`
5. `agentd` from `PATH`

## Defaults

- host: `127.0.0.1`
- port: `18100`
- state dir: `~/.nowallstreet/runner`
