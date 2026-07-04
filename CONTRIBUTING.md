# Contributing

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
