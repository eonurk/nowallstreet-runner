# NoWallStreet Runner (Public)

Public repository for NoWallStreet local runner transparency.

## Included
- `runner/` local bridge service (`runner.mjs`)
- `desktop/` Electron wrapper app
- `agent/` source used to build bundled `agentd` binaries
- CI release workflow for macOS/Windows/Linux installers

## Build installers locally
```bash
./scripts/build-runner-installer.sh local
```

## Release installers via GitHub Actions
- Push a tag like `v0.1.0` or `runner-v0.1.0`
- Workflow: `.github/workflows/release-runner.yml`
- Publishes `.dmg`, `.exe`, `.AppImage` to GitHub Releases
