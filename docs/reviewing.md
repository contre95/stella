---
title: "Reviewing"
description: "How Stella decides what to show you, and when."
weight: 50
toc: true
draft: false
---

Stella schedules cards with a Go port of [Mnemosyne](https://github.com/mnemosyne-proj/mnemosyne)'s
SM2 algorithm. After each card you grade how well you remembered it, from 0 (blackout) to 5
(perfect recall), and the grade sets when you see the card next: well-remembered cards come
back at ever longer intervals, forgotten ones come back soon.

## Settings

The `review` section of the [config](config.md) tunes the scheduler:

| Setting | Default | What it does |
| --- | --- | --- |
| `dayStartsAt` | `3` | Hour (0-23) at which a new study day begins, so a late-night session still counts for the day before |
| `nonMemorisedCardsInHand` | `10` | How many not-yet-retained cards are juggled at once |
| `maxScheduledIntervalDays` | `0` | Cap on the scheduled interval in days; 0 means no cap |
| `randomiseScheduledCards` | `false` | Shuffle due cards instead of ordering them by interval |
| `randomiseNewCards` | `false` | Shuffle the order in which unseen cards are introduced |

Day boundaries use the top-level `timezone` setting (an IANA name such as
`Europe/Madrid`), or the host's local timezone when it is empty.
