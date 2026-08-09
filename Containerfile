FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o stella       ./src/main.go

# ---- css ----
# Compiles public/css/app.css fresh from public/css/input.css + the views
# actually being shipped, via the Tailwind standalone CLI (no Node/npm).
# A compiled copy is also committed to git so `go run` works without this
# stage, but this is the source of truth for what ships in the image.
FROM alpine:3.20 AS css-builder

ARG TARGETARCH=amd64

RUN apk add --no-cache curl
RUN case "$TARGETARCH" in \
      amd64) TW_ARCH=x64 ;; \
      arm64) TW_ARCH=arm64 ;; \
      *) echo "unsupported arch for tailwindcss: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -sL -o /usr/local/bin/tailwindcss \
      "https://github.com/tailwindlabs/tailwindcss/releases/download/v3.4.19/tailwindcss-linux-${TW_ARCH}" \
    && chmod +x /usr/local/bin/tailwindcss

WORKDIR /build
COPY tailwind.config.js ./
COPY styles ./styles
COPY views ./views
RUN mkdir -p public/css \
    && tailwindcss -c tailwind.config.js -i styles/input.css -o public/css/app.css --minify

# ---- runtime ----
FROM alpine:3.20

ARG IMAGE_TAG
ENV IMAGE_TAG=$IMAGE_TAG

RUN apk add --no-cache sqlite-libs ca-certificates

WORKDIR /app
COPY --from=builder     /build/stella          ./stella
COPY --from=builder     /build/public          ./public
COPY --from=css-builder /build/public/css/app.css ./public/css/app.css
COPY --from=builder     /build/views           ./views
COPY config.example.yaml               ./config.yaml

RUN mkdir -p /app/data

# Web UI (MCP, when enabled, is served on the same port at /mcp)
EXPOSE 8858

ENTRYPOINT ["./stella"]
