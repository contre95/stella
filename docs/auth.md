---
title: "Users & login"
description: "Single-user mode, or multi-user with any OpenID Connect provider."
weight: 30
toc: true
draft: false
---

Stella runs in one of two modes, controlled by `auth.enabled`.

## Single user

`auth.enabled: false` (the default). There is no login, and all data belongs to one built-in
local account. The `/mcp` endpoint (when enabled) has no auth either, so put it behind a
trusted network (Tailscale, localhost, VPN) if you expose it.

This is meant for the lone self-hoster who is the only user of their services.

## Multi user

`auth.enabled: true`. Login goes through any OpenID Connect provider (Keycloak, Authentik,
Pocket ID, ...) configured under `auth.oidc`, and each user gets their own decks, cards and
review history. This mode is for self-hosters who want to share Stella with friends and
family.

```yaml
auth:
  enabled: true
  publicURLs:
    - https://stella.example.com
    - http://192.168.1.50:8858
  oidc:
    issuer: https://id.example.com
    clientID: stella
    clientSecret: ...
```

- `publicURLs` lists the external base URLs Stella is reachable at: usually one, but list
  more (a LAN address and a public domain, say) if you reach it several ways. Requests are
  matched to one of them by `Host` header, falling back to the first. Register
  `<publicURL>/auth/callback` as a redirect URI at the provider for each one.
- `oidc.issuer` is the provider's issuer URL; its discovery document must live at
  `<issuer>/.well-known/openid-configuration`.
- `oidc.cimd: true` makes Stella register itself through a self-hosted Client ID Metadata
  Document (`<publicURL>/auth/client-metadata.json`) instead of `clientID`/`clientSecret`:
  no client record to create, just allowlist that URL at the provider for each public URL.
  Only providers implementing CIMD (such as Pocket ID) support it, and it can't be combined
  with `clientID`/`clientSecret`.

## Tokens

`/settings/tokens` creates bearer tokens for [MCP clients](mcp.md) and
[sync devices](device-sync.md), and lists the OAuth apps connected to your account so you
can revoke them. A token is shown only once, when it is created.
