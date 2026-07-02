FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o stella       ./src/main.go

# ---- runtime ----
FROM alpine:3.20

ARG IMAGE_TAG
ENV IMAGE_TAG=$IMAGE_TAG

RUN apk add --no-cache sqlite-libs ca-certificates

WORKDIR /app
COPY --from=builder /build/stella      ./stella
COPY --from=builder /build/public      ./public
COPY --from=builder /build/views       ./views
COPY config.example.yaml               ./config.yaml

RUN mkdir -p /app/data

# Web UI
EXPOSE 8858

COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

ENTRYPOINT ["/docker-entrypoint.sh"]
