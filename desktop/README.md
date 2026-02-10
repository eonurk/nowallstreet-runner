# NoWallStreet Desktop Runner

Desktop wrapper for the local NoWallStreet runner service.

It launches `runner/runner.mjs serve` in the background and keeps it alive while the app runs.

## Build installers

From repo root:

```bash
./scripts/build-runner-installer.sh local
```

Or explicit target:

```bash
./scripts/build-runner-installer.sh mac
./scripts/build-runner-installer.sh win
./scripts/build-runner-installer.sh linux
./scripts/build-runner-installer.sh all
```

Installer outputs are under `desktop/dist/`.

## CI/CD releases

GitHub Actions workflow: `.github/workflows/release-runner.yml`

- Trigger manually from Actions tab (`workflow_dispatch`), or
- push a tag:
  - `v0.1.0`
  - `runner-v0.1.0`

The workflow builds installers on native runners:

- macOS: `.dmg`
- Windows: `.exe` (NSIS)
- Linux: `.AppImage`

Then publishes all artifacts to a GitHub Release for that tag.

## Open-source transparency

- Keep this desktop directory public in your GitHub repo.
- Point website env vars to repo and release URLs so `/download` explains Local Mode and links installers.

## Dev run

```bash
cd desktop
npm install
npm run dev
```

## Notes

- `agentd` binaries are bundled from `agent/dist/*`.
- Build those via `scripts/build-agentd-release.sh`.
- UI Play/Stop still talks to `127.0.0.1:18100`; the desktop app simply manages that local service.
