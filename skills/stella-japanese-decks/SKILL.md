---
name: stella-japanese-decks
description: Conventions for creating and editing Japanese flashcards in Stella via the Stella MCP — the colour code for furigana, particles, auxiliaries and adjective inflection, ruby syntax, {tts:ja} read-aloud spans, {center} usage, spacing rules and the standard card archetypes. Use when adding, editing or reviewing cards in any Japanese deck.
version: 1.3.0
---

# Japanese decks

Builds on the general card format and tool catalog in the `stella-cards` skill.

## Workflow

- Show the proposed cards in chat and wait for explicit OK before calling `create_card`, `create_cards` or `edit_card`.
- File Japanese decks under the Japonés group. Resolve its id with `list_groups` — never hardcode.
- Glosses and explanations in Spanish rioplatense (voseo: "¿qué querés?", "estudiás").
- Raw HTML is escaped by the renderer and shows literally on the card. Never emit `<br>`, `<span>` or any tag. A single newline is already a hard line break.

## Colour code

Mandatory and exhaustive. No other colours in a Japanese deck.

| Hex | Applies to |
|---|---|
| `{#ff6600}` | furigana reading — always inside the reading slot |
| `{#ff0000}` | particles: は が を に で と の か も へ や から まで より ね よ |
| `{#cc99ff}` | **auxiliaries** (助動詞) — the copula and anything attached to a stem: です でした な ではありません ではありませんでした ます ません ました ませんでした ましょう た て ている ています たい あります ありません |
| `{#22aa55}` | **i-adjective inflection** (活用語尾) — the tail of the adjective itself: い く くない かった くなかった |
| `{#94ABFF}` | grammar-pattern placeholder slots: `[Sustantivo]` `[Verbo]` `[Raíz]` `[Persona]` `[Lugar]` |

Constraints:

- Exactly 6 hex digits. No `#abc` shorthand, no named colours.
- `{/}` closes a colour span **only**. It does not close `{tts:…}` or `{center}`.
- Every `{#hex}` needs its own `{/}`. A missing closer does not error — it silently colours the rest of the card.

### Auxiliary vs inflection

Purple and green mark two genuinely different things, and keeping them apart is the point of the split:

- **Purple** is a separate bound word riding on a finished stem. です is a copula; ます is a politeness auxiliary; た, て, ている, たい stack on top of a verb stem.
- **Green** is the adjective changing shape by itself. Nothing is attached — 寒い *is* the predicate.

The payoff shows up in adjacent forms:

```
{寒|{#ff6600}さむ{/}}{#22aa55}い{/}                 frío — the adjective inflects
{静|{#ff6600}しず{/}}か{#cc99ff}な{/}                tranquilo — na-adjective takes the copula
{寒|{#ff6600}さむ{/}}{#22aa55}い{/}{#cc99ff}です{/}    two separate things, two colours
```

That last line is why the split exists. In 寒いです the い is inflection and the です is a grammatically vacuous politeness marker — one colour would imply they are one unit.

**Never split a single auxiliary across two colours.** たい and ない inflect like i-adjectives, but they are auxiliaries: colour the whole thing purple, even when inflected.

```
{食|{#ff6600}た{/}}べ{#cc99ff}たくない{/}        ✓ one auxiliary, one colour
{食|{#ff6600}た{/}}べ{#cc99ff}た{/}{#22aa55}くない{/}   ✗ splits たくない mid-word
```

## Spacing

**Japanese text never contains spaces between words.** This holds for every sentence on every card, in every deck, without exception — kanji-bearing sentences and all-kana beginner sentences alike. Japanese is not written with word separators, and spacing kana "for legibility" teaches a word boundary that does not exist on the page the learner will eventually read.

The rule survives colour spans: closing a `{/}` and opening the next word is not a reason to insert a space.

```
{tts:ja}にほん{#ff0000}の{/}えいが{#ff0000}が{/}すき{#cc99ff}です{/}。{/tts}          ✓
{tts:ja}にほん {#ff0000}の{/} えいが {#ff0000}が{/} すき {#cc99ff}です{/}。{/tts}   ✗ spaced word boundaries
```

- No space before or after a particle, an auxiliary, or an inflected tail.
- No space around 。 、 ？ ！ — the full-width punctuation already carries its own trailing space visually.
- Line breaks are the only permitted separator, and they separate whole examples, not words within one.
- Spaces are fine in the Spanish gloss, which lives outside the `{tts:ja}` span.

A stray space inside a `{tts:ja}` span is also an audio bug: the player may treat it as a phrase boundary and insert an unnatural pause mid-sentence.

## Furigana

Ruby is brace-pipe: `{base|reading}`. The pipe splits it into two slots — base before, reading after. The reading slot becomes the small text above the kanji. A markdown link `[漢字](かんじ)` is wrong — it renders as a link.

The reading is always coloured, inside the reading slot only:

```
{漢字|{#ff6600}かんじ{/}}     ✓ colour inside the reading slot
{#ff6600}{漢字|かんじ}{/}     ✗ colours the base kanji too
```

Okurigana stays outside the ruby — annotate only the kanji itself:

```
{寒|{#ff6600}さむ{/}}{#22aa55}い{/}
{飲|{#ff6600}の{/}}み{#cc99ff}ます{/}
```

**Furigana goes on the back only. Never on the front.** This holds for every archetype without exception — a single kanji, a compound, or a full sentence. The front carries the bare Japanese; the reading is what the card tests, so revealing it before the flip defeats the card. The back repeats the same text fully annotated.

This interacts with `{tts:ja}` — see below. A bare kanji inside a read-aloud span is spoken with a guessed reading, which is often wrong. Where the front must be both unannotated and audible, prefer leaving the front untts'd and putting the annotated, tts-wrapped version on the back.

## Read-aloud spans

```
{tts:ja} … {/tts}
```

- `ja` and `en` are the only accepted languages. Anything else stays literal text.
- The span wraps **only the Japanese run**. Never a Spanish gloss, never a heading, never a section label.
- One span per sentence. Three sentences means three spans.
- **Furigana must sit inside the span.** The player replaces each ruby with its reading before speaking, so an annotated kanji is read exactly as written. A bare kanji inside `{tts:ja}` is read with whatever reading the system voice guesses — frequently the wrong one. Since furigana is back-only, reliable audio is a back-side property.
- Closer is `{/tts}`, not `{/}`.
- Only the language is settable. Voice, rate and pitch are not configurable.
- `audioFront` / `audioBack` are a separate mechanism for pre-recorded audio files. Do not use them for Japanese — `{tts:ja}` is the convention.

## Nesting order

Fixed, outside-in:

```
{tts:ja}  →  {漢字|{#ff6600}かな{/}}   {#ff0000}partícula{/}   {#cc99ff}auxiliar{/}   {#22aa55}flexión{/}
```

Worked example:

```
{tts:ja}{麦茶|{#ff6600}むぎちゃ{/}}{#ff0000}を{/}{飲|{#ff6600}の{/}}み{#cc99ff}ます{/}{/tts}
```

## {center}

- Fronts already render centered. Backs render justified.
- Therefore: **wrap the entire back in one `{center}…{/center}`. Never put `{center}` on a front.**
- One block per face — the parser takes the first `{center}` and the last `{/center}`.
- A colour, ruby or tts span may not straddle the boundary.
- An unclosed `{center}` renders as literal text.

## Card archetypes

**Three example sentences on every content card.** Each example is one `{tts:ja}` span followed by its Spanish gloss.

Every front is bare: no furigana, no `{center}`. Section labels are the deck's own choice — what follows specifies the slots, not the wording.

### Kanji recognition

- **Front:** the bare kanji or compound. Nothing else.
- **Back:**
  - the compound with its reading, plus the meaning
  - the target kanji on its own with its reading and meaning
  - three example sentences

### Grammar pattern

- **Front:** the pattern, with `{#94ABFF}` placeholder slots and the ending being taught in its own colour — purple for an auxiliary, green for an adjective tail.
- **Back:** what it means, how it is formed, three examples.

### Sentence / reading

- **Front:** the sentence in bare Japanese, written with its kanji. No furigana.
- **Back:** the same sentence repeated with full furigana and tts, then the translation, then a breakdown of the vocabulary or the grammar point that makes the line worth a card.

### Conjugation

- **Front:** the headword in bare Japanese.
- **Back:** the headword annotated, then one tts-wrapped line per form, each followed by its gloss. Colour only the part that changes, in the colour its category calls for:

```
{tts:ja}{寒|{#ff6600}さむ{/}}{#22aa55}い{/}{/tts} = frío
{tts:ja}{寒|{#ff6600}さむ{/}}{#22aa55}くない{/}{/tts} = no es frío
{tts:ja}{寒|{#ff6600}さむ{/}}{#22aa55}かった{/}{/tts} = era frío
{tts:ja}{静|{#ff6600}しず{/}}か{#cc99ff}な{/}{/tts} = tranquilo (antes de sustantivo)
{tts:ja}{静|{#ff6600}しず{/}}か{#cc99ff}です{/}{/tts} = es tranquilo
```

### Kana

- **Front:** the kana.
- **Back:** the romaji, wrapped in `{center}`.
- No colours, no tts, no furigana.

## Gotchas

- `{braces without a pipe}` stay literal — they are not ruby.
- Math is `$inline$` and `$$display$$`.
- `audioFront` and `audioBack` swap sides on reverse cards.
- Furigana flattens to `漢字（かんじ）` on plain-text clients such as the x4 e-ink device. Colour, tts, center and math are stripped entirely there.
- Spacing: see the Spacing section — no spaces between Japanese words, ever, in any deck.
