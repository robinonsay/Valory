#!/bin/sh
set -e

if [ -z "$ACME_DOMAIN" ] && [ ! -f /tmp/dev-server.crt ]; then
  openssl req -x509 -nodes -days 365 \
    -newkey rsa:2048 \
    -keyout /tmp/dev-server.key \
    -out /tmp/dev-server.crt \
    -subj "/CN=localhost"
fi

exec /app/valory
