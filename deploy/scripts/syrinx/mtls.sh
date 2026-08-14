#!/bin/bash
# Optional public edge: nginx terminates TLS itself on 443 and hard-rejects
# any connection that doesn't present a client certificate signed by a
# locally-generated CA (ssl_verify_client on) — an alternative to the
# Cloudflare Tunnel edge, for operators who can port-forward 443 and want
# rejection enforced at the TLS layer, before any request reaches nginx's
# location blocks or the Go app behind it.
#
# Usage (as root):
#   source mtls.sh
#   mtls_install <app_domain>
#   mtls_issue_client_cert <name>
#   mtls_remove
#
# Requires before sourcing: APP_NAME, ENV_DIR (only used to derive paths)

MTLS_DIR_DEFAULT="/etc/${APP_NAME:-app}/mtls"
MTLS_DIR="${MTLS_DIR:-$MTLS_DIR_DEFAULT}"
MTLS_CLIENTS_DIR="$MTLS_DIR/clients"
MTLS_CA_KEY="$MTLS_DIR/ca.key"
MTLS_CA_CRT="$MTLS_DIR/ca.crt"
MTLS_SERVER_KEY="$MTLS_DIR/server.key"
MTLS_SERVER_CRT="$MTLS_DIR/server.crt"

mtls_dir_init() {
    mkdir -p "$MTLS_DIR" "$MTLS_CLIENTS_DIR"
    chmod 700 "$MTLS_DIR" "$MTLS_CLIENTS_DIR"
}

# Idempotent: only generates a CA if one doesn't already exist. The CA key
# never leaves the box — it's what lets this host mint/revoke client certs
# on its own, without a third-party CA or ACME dependency.
mtls_generate_ca() {
    if [ -f "$MTLS_CA_KEY" ] && [ -f "$MTLS_CA_CRT" ]; then
        return 0
    fi
    echo "🔐 Generating mTLS CA (10-year self-signed root, kept local to this host)..."
    openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
        -keyout "$MTLS_CA_KEY" -out "$MTLS_CA_CRT" \
        -subj "/CN=${APP_NAME:-app} mTLS CA" >/dev/null 2>&1
    chmod 600 "$MTLS_CA_KEY"
    chmod 644 "$MTLS_CA_CRT"
}

# Idempotent: regenerates the server cert only if missing, or if its SAN no
# longer matches the current domain (same drift-repair spirit as
# ensure_env_kv elsewhere in setup.sh/update.sh).
mtls_generate_server_cert() {
    local domain="$1"
    [ -n "$domain" ] || { echo "❌ mtls_generate_server_cert requires <domain>" >&2; return 1; }

    if [ -f "$MTLS_SERVER_CRT" ] && [ -f "$MTLS_SERVER_KEY" ] \
        && openssl x509 -in "$MTLS_SERVER_CRT" -noout -ext subjectAltName 2>/dev/null \
            | grep -q "DNS:${domain}\$\|DNS:${domain},"; then
        return 0
    fi

    echo "🔐 Generating nginx server certificate for ${domain}..."
    local csr
    csr="$(mktemp)"
    local ext_file
    ext_file="$(mktemp)"
    cat > "$ext_file" <<EOF
subjectAltName=DNS:${domain}
extendedKeyUsage=serverAuth
EOF

    openssl req -newkey rsa:2048 -sha256 -nodes \
        -keyout "$MTLS_SERVER_KEY" -out "$csr" \
        -subj "/CN=${domain}" >/dev/null 2>&1
    openssl x509 -req -in "$csr" -CA "$MTLS_CA_CRT" -CAkey "$MTLS_CA_KEY" -CAcreateserial \
        -days 825 -sha256 -extfile "$ext_file" -out "$MTLS_SERVER_CRT" >/dev/null 2>&1

    rm -f "$csr" "$ext_file"
    chmod 600 "$MTLS_SERVER_KEY"
    chmod 644 "$MTLS_SERVER_CRT"
}

# Mints a new client certificate signed by the CA. Bundles key + cert + CA
# into one directory the operator pulls down with cp-client-cert.sh — never
# printed to the terminal or a log, same convention as the root-identity
# export passphrase in root-bootstrap.sh.
mtls_issue_client_cert() {
    local name="$1"
    [ -n "$name" ] || { echo "❌ mtls_issue_client_cert requires <name>" >&2; return 1; }
    case "$name" in
        *[!a-zA-Z0-9_-]*|"") echo "❌ Client cert name must be alphanumeric/dash/underscore only" >&2; return 1 ;;
    esac

    local out_dir="$MTLS_CLIENTS_DIR/$name"
    if [ -f "$out_dir/client.crt" ]; then
        echo "🔹 Client cert '$name' already exists at $out_dir — skipping (delete the dir to reissue)"
        return 0
    fi

    mkdir -p "$out_dir"
    chmod 700 "$out_dir"

    local csr
    csr="$(mktemp)"
    openssl req -newkey rsa:2048 -sha256 -nodes \
        -keyout "$out_dir/client.key" -out "$csr" \
        -subj "/CN=${name}" >/dev/null 2>&1
    openssl x509 -req -in "$csr" -CA "$MTLS_CA_CRT" -CAkey "$MTLS_CA_KEY" -CAcreateserial \
        -days 825 -sha256 -extfile <(printf 'extendedKeyUsage=clientAuth\n') \
        -out "$out_dir/client.crt" >/dev/null 2>&1
    rm -f "$csr"

    cp "$MTLS_CA_CRT" "$out_dir/ca.crt"
    chmod 600 "$out_dir/client.key" "$out_dir/client.crt"
    chmod 644 "$out_dir/ca.crt"

    echo "✅ Client cert '$name' issued: $out_dir"
    echo "   Fetch with: ./cp-client-cert.sh $name"
}

# Writes the mTLS server block for the site config. Caller embeds this in
# place of the existing loopback-only `listen 127.0.0.1:$STATIC_PORT` block
# — the location blocks (/api/, /ws/, /_app/, /) are unchanged and passed
# in by the caller, not duplicated here.
mtls_nginx_listen_block() {
    local domain="$1"
    cat <<EOF
    listen 443 ssl;
    server_name ${domain};

    ssl_certificate     ${MTLS_SERVER_CRT};
    ssl_certificate_key ${MTLS_SERVER_KEY};
    ssl_client_certificate ${MTLS_CA_CRT};
    ssl_verify_client on;
EOF
}

mtls_nginx_redirect_block() {
    local domain="$1"
    cat <<EOF
server {
    listen 80;
    server_name ${domain};
    return 301 https://\$host\$request_uri;
}
EOF
}

# Top-level entry point: ensure dir, CA, and server cert all exist/match.
# Safe to call every run (setup.sh and update.sh) — idempotent throughout.
mtls_install() {
    local domain="$1"
    [ -n "$domain" ] || { echo "❌ mtls_install requires <app_domain>" >&2; return 1; }

    mtls_dir_init
    mtls_generate_ca
    mtls_generate_server_cert "$domain"
    echo "✅ mTLS edge ready: CA + server cert in $MTLS_DIR"
}

# Removes generated material entirely — used when switching a host away
# from EDGE_MODE=mtls. Does not touch nginx's site config; the caller
# regenerates that separately when EDGE_MODE changes.
mtls_remove() {
    if [ -d "$MTLS_DIR" ]; then
        rm -rf "$MTLS_DIR"
        echo "✅ mTLS material removed ($MTLS_DIR)"
    fi
}
