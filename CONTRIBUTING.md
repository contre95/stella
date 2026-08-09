# Contributing

## Stack

- Go 1.25+ · Fiber v2 · `html/template`
- HTMX + Hyperscript, Tailwind (UI components from [HyperUI](https://www.hyperui.dev/))
- SQLite (`mattn/go-sqlite3`), no migration framework
- YAML config + `validator/v10`

## Dev environment (devenv, optional)

If you use [devenv](https://devenv.sh) + Nix, `devenv shell` (or `devenv up`
to also run the server) drops you into a shell with Go and the Tailwind CLI
on `PATH`, and rebuilds `public/css/app.css` automatically on entry. Nothing
else below is needed in that case.

## CSS (Tailwind)

`public/css/app.css` is a **generated file** — compiled from `styles/input.css`
(hand-written base/component CSS + the three `@tailwind` directives) plus
whatever utility classes `tailwind.config.js` finds used under `views/**/*.html`.
Edit `styles/input.css`, not `public/css/app.css` directly.

Without devenv, compile it with the Tailwind standalone CLI (no Node/npm needed):

```sh
# one-time: download the CLI (matches the version pinned in Containerfile)
curl -sL -o tailwindcss \
  https://github.com/tailwindlabs/tailwindcss/releases/download/v3.4.19/tailwindcss-linux-x64  # or -arm64, -macos-x64, -macos-arm64
chmod +x tailwindcss

# rebuild after editing styles/input.css or any class names in views/
./tailwindcss -i styles/input.css -o public/css/app.css --minify

# or watch during development
./tailwindcss -i styles/input.css -o public/css/app.css --watch
```

`public/css/app.css` is committed so a fresh clone can `go run` immediately
without installing the CLI — but if you touch `styles/input.css` or add/remove
Tailwind classes in `views/`, regenerate and commit the updated `app.css` in
the same change. The `Containerfile` also recompiles it from scratch during
the image build, so production never depends on the committed copy being
up to date — only local `go run` does.

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
