---
title: "Device sync"
description: "Study offline on an Xteink X4 e-ink device and sync with Stella."
weight: 80
toc: true
draft: false
---

[stella-x4](https://codeberg.org/monopieza/stella-x4) is a standalone spaced-repetition
firmware for the Xteink X4 e-ink reader. It runs the same SM2 review loop offline, from the
SD card, and syncs with a Stella server over WiFi.

## Connecting a device

1. Create a token in `/settings/tokens`.
2. Give the device your Stella URL and that token (see the stella-x4 README for how).

The device then syncs in explicit sessions. Each session uploads the reviews done on the
device, downloads any decks that changed on the server, and exchanges scheduling state in
both directions (the most recent change wins).

## What syncs

- Server decks show up on the device as read-only mirrors. Editing, deleting, and choosing
  which decks to study happen on the server.
- Reviews done on the device land in the server's review history, and are safe to upload
  more than once.
- Cards reach the device as plain text: the [card formatting](cards.md#content-format) is
  stripped, furigana becomes `base（reading）`.

The sync API lives under `/api/x4` and only accepts bearer tokens.
