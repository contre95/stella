---
title: "Configuration"
description: "Every setting in config.yaml."
weight: 20
toc: true
draft: false
---

Stella reads `config.yaml` from the working directory, or from the path in
`STELLA_CONFIG_PATH`. This is the full example config (`config.example.yaml` in the repo),
with every setting at its default:

```yaml
dbPath: ./data/stella.db
# Directory where uploaded card images are stored.
imagePath: ./data/images
# IANA timezone (e.g. Europe/Madrid) used for review-day boundaries and sent
# to sync devices so their clocks show local time, DST included.
# Empty = the host's local timezone.
timezone: ""
server:
  port: 8858
  appName: Stella
  bodyLimit: 10485760
auth:
  enabled: false
  publicURLs: []
  oidc:
    issuer: ""
    clientID: ""
    clientSecret: ""
    cimd: false
mcp:
  # Enable the MCP endpoint at /mcp (StreamableHTTP transport).
  enabled: false
  refreshTokenDays: 30
logger:
  level: info   # debug | info | warn | error
  format: ""    # logfmt (default) | text | json
review:
  # Hour (0-23) at which a new study day begins.
  dayStartsAt: 3
  # How many not-yet-retained cards to juggle at once.
  nonMemorisedCardsInHand: 10
  # Cap on the scheduled interval in days; 0 = no cap.
  maxScheduledIntervalDays: 0
  # Shuffle due cards instead of ordering by interval.
  randomiseScheduledCards: false
  # Shuffle the order unseen cards are introduced.
  randomiseNewCards: false
cards:
  # Automatically play audio when a card (front or back) is shown.
  autoplayAudio: true
```

- `auth` is explained in [Users & login](auth.md).
- `mcp` is explained in [MCP server](mcp.md).
- `review` tunes the scheduler, see [Reviewing](reviewing.md).
- `server.bodyLimit` is the largest request body in bytes (10 MiB by default), which caps
  the size of uploaded media and [imported](import-export.md) archives.
