<img src="./public/logo.png" alt="Stella" width="140" align="right" />

# Stella

A self-hosted spaced-repetition web app. The scheduling engine is
a faithful port of [Mnemosyne](https://github.com/mnemosyne-proj/mnemosyne)'s
SM2 algorithm, rewritten in Go and wrapped in a small HTMX + Tailwind web UI.

## Build

```bash
# Container image
IMAGE_TAG=stella podman build -t stella .
```

## Run locally

With [devenv](https://devenv.sh) + Nix:

```bash
devenv up   # Go + Tailwind CLI provided, CSS rebuilt on shell entry, runs the server
```

Otherwise:

```bash
# Web UI on :8858 (MCP, when enabled, is served at /mcp)
go run ./src
```

Configuration lives in `config.yaml` (see `config.example.yaml`); the path is
overridable via `STELLA_CONFIG_PATH`.

`public/css/app.css` is a generated file (Tailwind, compiled from
`styles/input.css`) and a compiled copy is committed so this works out of the
box. If you edit styles or template class names, see `CONTRIBUTING.md` for
rebuilding it (or just use devenv, which does it automatically).

## Users & login

Stella runs in one of two modes, controlled by `auth.enabled`:

- `enabled: false` (default) — single-user, no login. All data belongs to one
  built-in local account. The `/mcp` endpoint (when enabled) has no auth
  either — put it behind a trusted network (Tailscale, localhost, VPN) if you
  expose it.
- `enabled: true` — multi-user. Login goes through any OpenID Connect provider
  (Keycloak, Authentik, Pocket ID, ...) configured under `auth.oidc`, and each
  user gets their own decks, cards, and review history. Register
  `<publicURL>/auth/callback` as the redirect URI at the provider.

  `/mcp` accepts two kinds of credential in this mode:
  - A static bearer token from `/settings/tokens` — paste it into the MCP
    client's config as `Authorization: Bearer <token>`.
  - OAuth: Stella acts as its own OAuth Authorization Server for MCP clients
    (dynamic client registration, PKCE, consent screen), layered on top of the
    OIDC login above. Point an OAuth-capable MCP client (Claude Desktop,
    Claude Code, Claude.ai custom connectors, ...) at `<publicURL>/mcp` and it
    discovers the flow automatically — no manual token needed. Connected apps
    can be revoked from `/settings/tokens`.
