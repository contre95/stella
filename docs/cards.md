---
title: "Cards"
description: "Decks, groups, tags, and the card content format."
weight: 40
toc: true
draft: false
---

## Data model

- **Group**: an optional folder for decks. A deck belongs to at most one group.
- **Deck**: a named collection of facts (name up to 32 characters, description, optional
  cover image, optional group).
- **Tag**: a free label; a deck can carry any number of them, independent of groups.
- **Fact**: the content itself, a `front` and a `back`, plus an optional image and audio
  file per side. A fact belongs to one deck.
- **Card**: one direction of quizzing derived from a fact. The **forward** card
  (front → back) always exists; the **reverse** card (back → front) is optional and needs a
  non-empty back.

Groups are one per deck (exclusive categories); tags are many per deck (cross-cutting
labels).

## Content format

Front and back are Markdown, hard-wrapped (one newline in the source is one line break on
the card), with Stella's own inline syntax on top. Raw HTML is never passed through. All of
these can be mixed within a side:

| Syntax | Renders as | Notes |
|---|---|---|
| `**bold**`, `*italic*`, `# heading`, `- item`, `1. item`, `[text](url)` | Normal Markdown | |
| `{#RRGGBB}text{/}` | Colored text | The hex must be exactly 6 digits |
| `{base\|reading}` | Ruby/furigana, e.g. `{漢字\|かんじ}` | No braces, pipes or newlines inside; doesn't nest |
| `{tts:en}text{/tts}`, `{tts:ja}text{/tts}` | A read-aloud button plus the text (browser speech synthesis, no audio file needed) | Only `en` and `ja`; the closing tag is `{/tts}`, not `{/}` |
| `{center}...{/center}` | A centered block | Sides are justified by default. A color, tts or furigana span can't straddle the boundary: nest it inside |
| `$formula$` | Inline math (KaTeX) | No `$` or newline inside |
| `$$formula$$` | Display math (KaTeX) | May span lines |

For example:

```
{center}What is the Pythagorean theorem?{/center}
```

```
{center}$$a^2 + b^2 = c^2$$

For a right triangle with legs $a$, $b$ and hypotenuse $c$.{/center}
```

```
{center}This combines **everything**: {#22aa55}color{/}, {tts:en}speech{/tts}, and {漢字|かんじ} furigana.{/center}
```

Plain-text clients, like the [e-ink sync device](device-sync.md), get all of this stripped to
bare text: furigana becomes `base（reading）`, and every other construct is unwrapped to its
inner content.

## Media

Each side can carry one image (jpg, png, gif, webp) and one audio file (mp3, ogg, wav, m4a,
webm, aac, flac). With `cards.autoplayAudio` on, audio plays as soon as its side is shown.
