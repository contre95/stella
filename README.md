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
- `src/features/<f>/` — config, logging, hosting, ui, cards, review
- `src/main.go` — composition root

## Run

```bash
go run ./src          # boots on :8080 (config.yaml auto-created on first run)
curl localhost:8080/health   # -> OK
```

Config path is overridable via `STELLA_CONFIG_PATH`.

## Status

Built bottom-up:

- [x] Step 1 — booting skeleton (config, logging, Fiber server, dashboard shell)
- [ ] Step 2 — SM2 algorithm port + golden tests
- [ ] Step 3 — SQLite repository
- [ ] Step 4 — cards feature (CRUD + sister cards)
- [ ] Step 5 — review feature (queue + grading)
- [ ] Step 6 — dashboard stats + polish
