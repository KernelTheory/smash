#!/bin/sh
# One-time setup of an nginx host serving smash releases over HTTPS (run with sudo on the server).
# ACME webroot on :80 (/var/www/certbot), Let's Encrypt certificate on :443. Safe to re-run.
# Prerequisite: DNS A records for every domain pointing at this server.
# SMASH_DOMAIN may list several names (the first one names the config and certificate):
#   scp deploy/server-setup.sh you@your-server:/tmp/ && ssh -t you@your-server sudo SMASH_DOMAIN="example.com www.example.com" sh /tmp/server-setup.sh
set -eu
domains="${SMASH_DOMAIN:-keysmash.website}"
domain="${domains%% *}"
owner="${SMASH_OWNER:-${SUDO_USER:?run with sudo, or set SMASH_OWNER}}" # the user who deploys, without sudo
root=/var/www/smash
conf=/etc/nginx/sites-available/$domain

# 1. Web root the deploy user can write without sudo.
mkdir -p "$root/releases"
chown -R "$owner:$owner" "$root"

# 2. HTTP-only block so certbot can answer the ACME challenge.
cat > "$conf" <<NGINX
server {
    listen 80;
    listen [::]:80;
    server_name $domains;
    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
        allow all;
    }
    location / {
        return 301 https://\$host\$request_uri;
    }
}
NGINX
ln -sfn "$conf" "/etc/nginx/sites-enabled/$domain"
nginx -t && systemctl reload nginx

# 3. Certificate (renewed by the existing certbot timer).
certbot certonly --webroot -w /var/www/certbot $(for d in $domains; do printf -- "-d %s " "$d"; done) --non-interactive --agree-tos --register-unsafely-without-email --keep-until-expiring

# 4. HTTPS block serving the static release files.
cat >> "$conf" <<NGINX
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name $domains;
    ssl_certificate     /etc/letsencrypt/live/$domain/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$domain/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;
    ssl_prefer_server_ciphers on;
    add_header Strict-Transport-Security "max-age=63072000" always;

    root $root;
    autoindex off;
    index index.html;
    location = /install.sh {
        default_type text/plain;
        add_header Cache-Control "no-cache" always;
        add_header Strict-Transport-Security "max-age=63072000" always;
    }
    location /releases/latest/ {
        add_header Cache-Control "no-cache" always;
        add_header Strict-Transport-Security "max-age=63072000" always;
    }
    location / {
        try_files \$uri \$uri/ =404;
    }
}
NGINX
nginx -t && systemctl reload nginx
echo "Ready: https://$domain/ (deploy with scripts/deploy.sh, no sudo needed)"
