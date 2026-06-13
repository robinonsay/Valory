# Build stage
FROM golang:1.25-bookworm AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/valory ./cmd/server

# Runtime stage
FROM debian:bookworm-slim

# Native dependencies for asciidoctor-mathematical, which converts STEM blocks
# to SVG math glyphs in PDF export (REQ-CONTENT-012). The mathematical gem
# requires cairo, pango, cmake, bison, flex, libffi, libxml2 and gdk-pixbuf
# for its C extension build. fonts-lyx provides the Latin Modern fonts that
# asciidoctor-mathematical uses for TeX rendering.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        openssl \
        ruby \
        ruby-dev \
        build-essential \
        cmake \
        bison \
        flex \
        libffi-dev \
        libxml2-dev \
        libgdk-pixbuf2.0-dev \
        libcairo2-dev \
        libpango1.0-dev \
        fonts-lyx \
    && gem install asciidoctor asciidoctor-pdf asciidoctor-mathematical \
          --no-document \
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
