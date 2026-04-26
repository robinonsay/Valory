# Build stage
FROM golang:1.23-bookworm AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/valory ./cmd/valory

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

ENTRYPOINT ["./entrypoint.sh"]
