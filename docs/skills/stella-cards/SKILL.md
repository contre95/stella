---
name: stella-cards
description: Reference for Stella's flashcard content format (Markdown plus color/furigana/text-to-speech/center/math syntax) and the MCP tools for creating and managing decks, groups, tags, and cards. Use when creating, editing, or bulk-importing Stella flashcards via MCP, or when asked what Stella's card format supports.
---

# Stella cards & decks

Stella is a self-hosted spaced-repetition app (SM2/Mnemosyne scheduling). Its MCP
server exposes tools (`create_card`, `list_decks`, ...) for creating and
managing content. This skill documents the card content format and the full
tool catalog so an agent can produce correct, richly-formatted cards without
trial and error.

## Data model

- **Group** — optional top-level folder for decks. A deck belongs to at most one group.
- **Deck** — a named collection of facts (name ≤ 32 chars, description, optional cover image, optional group).
- **Tag** — a free label a deck can carry any number of, independent of groups.
- **Fact** — the actual content: `front` + `back` text, plus an optional image and audio file per side. One fact belongs to one deck.
- **Card** — one direction of quizzing derived from a fact: the **forward** card (front → back) always exists once a fact has a front; the **reverse** card (back → front) is optional and requires non-empty `back`.

All create/edit tools operate at the fact level (`front`, `back`, `reverse`,
media, `grade`); forward/reverse "sister cards" are generated automatically —
there's no separate card-creation step.

## Card content format

`front`/`back` are Markdown (hard-wrapped: one newline in the source is one
line break on the card) plus Stella-specific inline syntax layered on top. Raw
HTML is never passed through. These can be freely mixed within a field:

| Syntax | Renders as | Notes |
|---|---|---|
| Standard Markdown: `**bold**`, `*italic*`, `# heading`, `- item`, `1. item`, `[text](url)` | Normal HTML | Goldmark, hard line breaks |
| `{#RRGGBB}text{/}` | Colored span | Hex must be exactly 6 digits |
| `{base\|reading}` | Ruby/furigana, e.g. `{漢字\|かんじ}` | No braces/pipes/newlines inside either run; doesn't nest |
| `{tts:en}text{/tts}` or `{tts:ja}text{/tts}` | Read-aloud button + text (browser speech synthesis, no audio file needed) | Only `en`/`ja`; closing tag is literally `{/tts}`, **not** `{/}` |
| `{center}...{/center}` | Centers a block | Faces are justified by default; typically wraps the whole side. A color/tts/furigana span can't straddle a `{center}`/`{/center}` boundary — nest the inner span first, then wrap it |
| `$formula$` | Inline math (KaTeX) | No `$` or newline inside |
| `$$formula$$` | Block/display math (KaTeX) | Non-greedy, may span lines |

Example (from Stella's own built-in sample deck):

```
front: {center}What is the Pythagorean theorem?{/center}
back:  {center}$$a^2 + b^2 = c^2$$\n\nFor a right triangle with legs $a$, $b$ and hypotenuse $c$.{/center}
```
```
front: {center}This combines **everything**: {#22aa55}color{/}, {tts:en}speech{/tts}, and {漢字|かんじ}furigana{/} in one {center} block.{/center}
```

Plain-text clients (e.g. the e-ink sync device) get all of the above stripped
to bare text: furigana becomes `base（reading）`, every other construct is
unwrapped to its inner content.

## Media

`imageFront`/`imageBack`/`audioFront`/`audioBack` accept **only http(s)
URLs** — never local paths (the server downloads and copies them into its own
media store; a "local path" would read the server's filesystem, not the
caller's). Images: jpg/png/gif/webp. Audio: mp3/ogg/wav/m4a/webm/aac/flac. On
`edit_card`, supplying a new URL replaces the current file; the matching
`removeImage*`/`removeAudio*` flag clears it instead.

## Tool catalog (MCP)

**Decks**
- `create_deck(name, description?, groupId?)` → deckId
- `list_decks()` → all decks with their tags
- `rename_deck(deckId, name, description?)`
- `delete_deck(deckId)` — cascades to all its facts/cards
- `set_deck_group(deckId, groupId?)` — omit/empty `groupId` to ungroup
- `set_deck_reverse(deckId, enabled)` — bulk add/remove the reverse card across every fact in the deck

**Groups**
- `create_group(name)` → groupId
- `list_groups()` → groups with member decks, plus ungrouped decks separately
- `rename_group(groupId, name)`
- `delete_group(groupId)` — member decks become ungrouped, not deleted

**Tags**
- `create_tag(name)` → idempotent, returns the existing id if the name is already used
- `list_tags()` → tags with the decks carrying each
- `rename_tag(tagId, name)`
- `delete_tag(tagId)` — decks just lose the label
- `tag_deck(deckId, tag)` — creates the tag by name if it doesn't exist yet
- `untag_deck(deckId, tag)`

**Cards**
- `create_card(front, deckId, back?, reverse?, grade?, imageFront?, imageBack?, audioFront?, audioBack?)` — one fact
- `create_cards(deckId, cards[])` — bulk; each item is the same shape as `create_card` minus `deckId`. Stops on the first error and reports how many were created before it
- `edit_card(factId, front?, back?, addReverse?, removeReverse?, image/audio URLs..., removeImage*/removeAudio* flags...)` — omitted fields keep their current value; scheduling data is untouched
- `move_card(factId, deckId)` — reassigns the deck only; scheduling untouched
- `delete_card(factId)` — cascades to sister cards
- `list_recent_cards(limit?)` → newest facts first, each with its sister cards' view/status and media filenames — use this to discover a `factId` and current content before edit/move/delete
- `get_card_image(factId, side?)` → the image(s) as inline content

## Workflow notes

- `deckId` is always required to create a card — call `list_decks` or `create_deck` first.
- `grade` (0–5) is optional; omit it to leave the card unseen for the normal review queue. 0 = blackout, 5 = perfect recall. Supplying it counts as a first repetition.
- `reverse: true` only produces a back → front card when `back` is non-empty.
- For importing several cards into one deck, prefer one `create_cards` call over repeated `create_card` calls.
- Groups are one-per-deck (exclusive categorization); tags are many-per-deck (cross-cutting labels). Reach for whichever matches the categorization you actually want.
- The MCP connection has no per-call auth beyond the connection itself — every call acts as the single account the server is bound to.
