# Stella

A single-user, self-hosted spaced-repetition web app. The scheduling engine is
a faithful port of [Mnemosyne](https://github.com/mnemosyne-proj/mnemosyne)'s
SM2 algorithm, rewritten in Go and wrapped in a small HTMX + Tailwind web UI.

## Stack

- Go 1.25+ · Fiber v2 · `html/template`
- HTMX + Hyperscript, Tailwind (UI components from [HyperUI](https://www.hyperui.dev/))
- SQLite (`mattn/go-sqlite3`), no migration framework
- YAML config + `validator/v10`

## Architecture

Clean / DDD layering (see `SCAFFOLD.md` in the sibling mnemosyne repo for the
full contract):

```
handlers (transport) -> service (logic) -> domain interfaces <- infra (adapters)
                                                  ^
                                  main.go wires concrete infra in
```

- `src/srs/` — pure domain: entities + the SM2 algorithm + repository interface
- `src/infra/database/` — SQLite implementation of `srs.Repository`
- `src/features/<f>/` — config, logging, hosting, ui, cards, review, mcp
- `src/main.go` — web composition root
- `src/cmd/mcp/` — MCP server composition root (a second binary)

## Run

### Locally (two processes)

```bash
# Terminal 1 — web UI on :8858
go run ./src

# Terminal 2 — MCP server on :8859
go run ./src/cmd/mcp
```

Both read the same `config.yaml` and share the same SQLite database. SQLite
serialises concurrent writes with a busy timeout so running them in parallel
is safe.

Config path is overridable via `STELLA_CONFIG_PATH`.

### Docker (both in one container)

```bash
# Build
docker build -t stella .

# Run — mount a volume so the database survives restarts
docker run -d \
  -p 8858:8858 \
  -p 8859:8859 \
  -v stella-data:/app/data \
  --name stella \
  stella
```

The entrypoint starts both binaries. If either crashes the container exits
(use `--restart unless-stopped` for production).

Port summary:

| Port | Service        |
|------|----------------|
| 8858 | Web UI (HTTP)  |
| 8859 | MCP (StreamableHTTP) |

### MCP server

The MCP server speaks the [Model Context Protocol](https://modelcontextprotocol.io)
over **StreamableHTTP** on port `8859`. Connect an MCP client to
`http://<host>:8859`.

For Claude Code (pointing at a local instance):

```bash
claude mcp add stella --transport http http://localhost:8859
```

Tools:

- `create_card` — make a fact and its sister cards. `front` is required; `back`,
  `reverse` (also quiz back→front), `grade` (0–5 initial self-grade; omit to leave
  unseen) and `deckId` are optional.
- `list_recent_cards` — list recent facts with each sister card's scheduling status.

## Status

Built bottom-up:

- [x] Step 1 — booting skeleton (config, logging, Fiber server, dashboard shell)
- [ ] Step 2 — SM2 algorithm port + golden tests
- [ ] Step 3 — SQLite repository
- [ ] Step 4 — cards feature (CRUD + sister cards)
- [ ] Step 5 — review feature (queue + grading)
- [ ] Step 6 — dashboard stats + polish
