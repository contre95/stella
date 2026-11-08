---
name: stella-vocabulary-decks
description: Conventions for creating and editing Spanish vocabulary flashcards in Stella via the Stella MCP — a single word on the front, and a back with a definition plus one highlighted example sentence in a fixed colour and emphasis shape. Use when adding, editing or reviewing cards in any vocabulary deck.
version: 1.0.0
---

# Vocabulary decks

Builds on the general card format and tool catalog in the `stella-cards` skill.

## Workflow

- Show the proposed cards in chat and wait for explicit OK before calling `create_card`, `create_cards` or `edit_card`.
- Resolve deck and group ids with `list_decks` / `list_groups` — never hardcode.
- Definitions in Spanish, dictionary register. Rioplatense where usage differs.
- Forward-only. Never set `reverse` — prompting definition-to-word is a different exercise and does not belong in this deck.
- Raw HTML is escaped by the renderer and shows literally on the card. Never emit `<br>` or `<span>`. A single newline is already a hard line break.

## Front

A single word, capitalised, prefixed with the 📘 emoji at the very start of the card (front): `📘 Modelo`. Nothing else — no markdown, no colour, no `{center}` (fronts already render centered).

Two permitted variants:

- Related forms joined with a slash: `📘 Ontología / Ontológico`
- An optional inflection in parentheses: `📘 Paroxismo(s)`

## Back

Fixed shape:

```
{center}
<definición>


Ejemplo:
_"{#9CA3AF}<oración con la {#FF0000}palabra{/} resaltada>{/}"_
{/center}
```

Reference rendering:

```
{center}
Modelo original o ejemplo ideal que sirve de patrón para representar una categoría de personas, objetos o conceptos (p. ej., "el héroe" como arquetipo narrativo).


Ejemplo:
_"{#9CA3AF}El mentor sabio es un {#FF0000}arquetipo{/} que aparece en incontables historias{/}"_
{/center}
```

Slot rules:

- **Definition** — one paragraph. A second sense or a figurative use is appended after `;` or `también,`. A clarifying instance goes in a parenthetical `(p. ej., …)`.
- **`Ejemplo:`** — the literal label, on its own line, followed by a line break rather than a blank line. The label and the sentence are one paragraph.
- **Sentence** — exactly one, in `_"…"_` (italic plus literal double quotes), entirely wrapped in `{#9CA3AF}`, with the headword wrapped in `{#FF0000}` nested inside. Inflect the headword to fit the sentence naturally and highlight the inflected form.

The double blank line before `Ejemplo:` is cosmetic. Consecutive blank lines collapse to a single paragraph break, so it renders the same as one — keep it for source legibility, and do not "fix" it.

## Colour palette

Mandatory and exhaustive. No other colours in a vocabulary deck.

| Hex | Applies to |
|---|---|
| `{#9CA3AF}` | the whole example sentence — de-emphasised grey |
| `{#FF0000}` | the headword inside the example sentence |

**This palette is not the Japanese-deck palette.** `{#FF0000}` means "target word" here and "particle" there. The two are not interchangeable; do not carry conventions across.

## Renderer constraints

- Colour spans nest. `{#9CA3AF}…{#FF0000}word{/}…{/}` produces a grey span containing a red span; the red wins on the highlighted word and grey resumes after it. Both closers are required.
- Every `{#hex}` needs its own `{/}`. `{/}` closes a colour span only — it is not `{/center}`.
- `{center}` wraps the whole back, once. The parser takes the first `{center}` and the last `{/center}`.
- A colour or emphasis span may not straddle the `{center}` boundary.
- Exactly 6 hex digits. Case is irrelevant to the parser; this deck writes them uppercase.

## Gotchas

- A missing `{/}` does not error. It leaves the span open and colours the remainder of the card. Count openers against closers before submitting.
- The italic markers sit outside the quotes and outside the colour spans: `_"` opens, `"_` closes.
