---
title: "Docs"
description: "Stella, a self-hosted spaced-repetition web app."
weight: 999
toc: true
draft: false
---

# Stella

A self-hosted spaced-repetition web app. The scheduling engine is a faithful port of
[Mnemosyne](https://github.com/mnemosyne-proj/mnemosyne)'s SM2 algorithm, rewritten in Go
and wrapped in a small HTMX + Tailwind web UI.

These pages are published at [stella.contre.io](https://stella.contre.io) from the `docs/`
folder of the [stella repository](https://github.com/contre95/stella). Start with
[Self hosted](self-host.md) to get it running, then [Configuration](config.md).

- [Users & login](auth.md): single-user or multi-user with OIDC.
- [Cards](cards.md): the card content format.
- [Reviewing](reviewing.md): how the review queue works.
- [Import & export](import-export.md): moving decks and groups between instances.
- [MCP server](mcp.md): letting an AI assistant manage your decks.
- [Device sync](device-sync.md): studying offline on an e-ink device.
