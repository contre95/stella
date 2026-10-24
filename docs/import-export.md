---
title: "Import & export"
description: "Move decks and groups between Stella instances as zip archives."
weight: 60
toc: true
draft: false
---

Decks and groups can be exported and imported as archives. An export carries the content,
the media, and each card's scheduling state and review history, so an imported deck resumes
where it left off.

## Exporting

Export a deck or a group from its page. The endpoints are:

- `GET /decks/{id}/export`: a deck zip
- `GET /groups/{id}/export`: a group zip

## Importing

Import from the decks page, by uploading a file or pasting one from the clipboard. Stella
accepts:

- a **deck zip**, with `deck.json` at the root and the media it references under `media/`
- a **group zip**, with `group.json` at the root and one deck folder per deck under `decks/`,
  each laid out like a deck zip
- a bare **deck JSON** file, in the same shape as `deck.json`

Archives are limited by `server.bodyLimit` (10 MiB by default, see [Configuration](config.md)).

## Writing a deck by hand

A hand-written `deck.json` only needs the content. Facts without scheduling data are created
as unseen cards, plus a reverse card when `reverse` is true:

```json
{
  "name": "Spanish",
  "description": "Everyday words",
  "facts": [
    { "front": "la manzana", "back": "apple", "reverse": true },
    { "front": "{tts:ja}こんにちは{/tts}", "back": "hello" }
  ]
}
```

See [Cards](cards.md) for what `front` and `back` can contain.
