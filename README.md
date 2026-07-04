# Stella

A single-user, self-hosted spaced-repetition web app. The scheduling engine is
a faithful port of [Mnemosyne](https://github.com/mnemosyne-proj/mnemosyne)'s
SM2 algorithm, rewritten in Go and wrapped in a small HTMX + Tailwind web UI.

## Build

```bash
# Container image
IMAGE_TAG=stella podman build -t stella .
```

## Run locally

```bash
# Terminal 1 — web UI on :8858
go run ./src

# Terminal 2 — MCP server on :8859
go run ./src/cmd/mcp
```

Both read the same `config.yaml` and share the same SQLite database. Config
path is overridable via `STELLA_CONFIG_PATH`.
