#!/bin/sh
set -e

if [ ! -f /etc/nginx/ssl/nginx.crt ]; then
  mkdir -p /etc/nginx/ssl
  openssl req -x509 -nodes -days 365 \
    -newkey rsa:2048 \
    -keyout /etc/nginx/ssl/nginx.key \
    -out /etc/nginx/ssl/nginx.crt \
    -subj "/CN=localhost"
fi

exec nginx -g "daemon off;"
