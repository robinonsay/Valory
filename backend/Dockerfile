# Build stage
FROM golang:1.23-bookworm AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/valory ./cmd/server

# Runtime stage
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        openssl \
        ruby \
    && gem install asciidoctor asciidoctor-pdf --no-document \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /app/valory ./valory
COPY backend/entrypoint.sh ./entrypoint.sh
RUN chmod +x ./entrypoint.sh

EXPOSE 8443
# Port 80 is used for ACME HTTP-01 challenge responses. It is not mapped to the
# host in docker-compose.yml because the frontend (Nginx) service occupies host:80
# and proxies /.well-known/acme-challenge/* to http://api:80 via Docker's internal
# network. No host port mapping is required for this internal service-to-service path.
EXPOSE 80

ENTRYPOINT ["./entrypoint.sh"]
