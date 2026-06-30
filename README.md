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

```bash
go run ./src          # boots on :8080 (config.yaml auto-created on first run)
curl localhost:8080/health   # -> OK
```

Config path is overridable via `STELLA_CONFIG_PATH`.

## MCP server

A second binary speaks the [Model Context Protocol](https://modelcontextprotocol.io)
over stdio, so an MCP client (Claude, editors, …) can create and browse cards. It
is a transport over the same cards service and reads the same `config.yaml`/SQLite
database as the web app.

```bash
go build -o stella-mcp ./src/cmd/mcp
```

Tools:

- `create_card` — make a fact and its sister cards. `front` is required; `back`,
  `reverse` (also quiz back→front), `grade` (0–5 initial self-grade; omit to leave
  unseen) and `deckId` are optional.
- `list_recent_cards` — list recent facts with each sister card's scheduling status.

Register it with a client by pointing at the built binary, e.g. for Claude Code:

```bash
claude mcp add stella -e STELLA_CONFIG_PATH=/abs/path/to/config.yaml -- /abs/path/to/stella-mcp
```

The MCP server and the web app can run at once against the same DB (SQLite serialises
writes with a busy timeout). Because the client controls the binary's working
directory, use **absolute** paths for `database.path` (and `dataPath`) in the
config you point `STELLA_CONFIG_PATH` at.

## Status

Built bottom-up:

- [x] Step 1 — booting skeleton (config, logging, Fiber server, dashboard shell)
- [ ] Step 2 — SM2 algorithm port + golden tests
- [ ] Step 3 — SQLite repository
- [ ] Step 4 — cards feature (CRUD + sister cards)
- [ ] Step 5 — review feature (queue + grading)
- [ ] Step 6 — dashboard stats + polish
