---
title: "MCP server"
description: "Let Claude or any MCP client create and manage your decks."
weight: 70
toc: true
draft: false
---

Stella can expose a [Model Context Protocol](https://modelcontextprotocol.io) server, so an AI
assistant can create decks and cards for you, tag and group them, and edit or move existing
cards.

```yaml
mcp:
  enabled: true
```

The endpoint is `<your Stella URL>/mcp` (StreamableHTTP transport), on the same port as the
web UI.

## Authentication

In [single-user mode](auth.md#single-user) `/mcp` has no auth at all: keep it on a trusted
network.

In [multi-user mode](auth.md#multi-user) it accepts two kinds of credential:

- **A bearer token** from `/settings/tokens`. Put it in the MCP client's config as
  `Authorization: Bearer <token>`.
- **OAuth.** Stella acts as its own OAuth Authorization Server for MCP clients (dynamic
  client registration, PKCE, a consent screen), on top of the OIDC login. Point an
  OAuth-capable client (Claude Desktop, Claude Code, Claude.ai custom connectors, ...) at
  `<publicURL>/mcp` and it discovers the flow by itself. Connected apps can be revoked from
  `/settings/tokens`. Refresh tokens stay valid for `mcp.refreshTokenDays` days (30 by
  default), after which the client goes through the consent screen again.

For example, with Claude Code:

```bash
claude mcp add --transport http stella https://stella.example.com/mcp
```

## Tools

| Area | Tools |
| --- | --- |
| Decks | `create_deck`, `list_decks`, `rename_deck`, `delete_deck`, `set_deck_group`, `set_deck_reverse` |
| Groups | `create_group`, `list_groups`, `rename_group`, `delete_group` |
| Tags | `create_tag`, `list_tags`, `rename_tag`, `delete_tag`, `tag_deck`, `untag_deck` |
| Cards | `create_card`, `create_cards`, `edit_card`, `move_card`, `delete_card`, `list_recent_cards`, `get_card_image` |

Media fields on `create_card` and `edit_card` take http(s) URLs only; Stella downloads the
files into its own media store.

## Agent skill

The repo ships agent skills under
[`skills/`](https://github.com/contre95/stella/tree/main/skills):

- [`stella-cards`](https://github.com/contre95/stella/blob/main/skills/stella-cards/SKILL.md) —
  the main skill: the [card format](cards.md) and every tool in detail, so an agent can write
  well-formatted cards without trial and error.
- [`stella-japanese-decks`](https://github.com/contre95/stella/blob/main/skills/stella-japanese-decks/SKILL.md) —
  conventions for Japanese decks (colour code, furigana, `{tts:ja}`, card archetypes).
- [`stella-vocabulary-decks`](https://github.com/contre95/stella/blob/main/skills/stella-vocabulary-decks/SKILL.md) —
  conventions for Spanish vocabulary decks.

Install them into your agent (for Claude Code, copy the folders into `~/.claude/skills/`).
