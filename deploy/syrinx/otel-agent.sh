#!/bin/bash
# App-host telemetry agent: journald logs, host metrics, and local OTLP ingress
# (Syrinx traces + app metrics on 127.0.0.1:4317/:4318) → telemetry Pi → OpenObserve.
#
# Usage (as root):
#   source otel-agent.sh
#   otel_agent_install <telemetry_host> <service_name>
#   otel_agent_remove

OTELCOL_AGENT_VERSION="${OTELCOL_AGENT_VERSION:-0.120.1}"
OTEL_AGENT_CONFIG_DIR="/etc/otelcol-agent"
OTEL_AGENT_CONFIG="$OTEL_AGENT_CONFIG_DIR/config.yaml"
OTEL_AGENT_UNIT="otelcol-agent.service"
OTEL_AGENT_BIN="/usr/local/bin/otelcol-contrib"

otel_agent_arch_tag() {
    local arch
    arch="$(dpkg --print-architecture 2>/dev/null || uname -m)"
    case "$arch" in
        arm64|aarch64) echo "arm64" ;;
        amd64|x86_64)  echo "amd64" ;;
        *)
            echo "❌ Unsupported arch for otelcol-contrib: $arch" >&2
            return 1
            ;;
    esac
}

otel_agent_ensure_persistent_journal() {
    # Empty /var/log/journal + volatile-only storage breaks otel journald
    # ("No journal boot entry found for the specified boot (+0)").
    mkdir -p /etc/systemd/journald.conf.d
    cat > /etc/systemd/journald.conf.d/persistent.conf <<EOF
[Journal]
Storage=persistent
SystemMaxUse=200M
EOF
    mkdir -p /var/log/journal
    systemd-tmpfiles --create --prefix /var/log/journal >/dev/null 2>&1 || true
    systemctl restart systemd-journald
    journalctl --flush >/dev/null 2>&1 || true
}

otel_agent_ensure_binary() {
    local tag tmp url ver_major
    tag="$(otel_agent_arch_tag)" || return 1
    ver_major="${OTELCOL_AGENT_VERSION%.*}"

    if [ -x "$OTEL_AGENT_BIN" ] && "$OTEL_AGENT_BIN" --version 2>/dev/null | grep -qE "${ver_major}\.|${OTELCOL_AGENT_VERSION}"; then
        return 0
    fi

    echo "⬇️  Installing otelcol-contrib v${OTELCOL_AGENT_VERSION} (${tag})..."
    tmp="$(mktemp -d)"
    url="https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v${OTELCOL_AGENT_VERSION}/otelcol-contrib_${OTELCOL_AGENT_VERSION}_linux_${tag}.tar.gz"
    curl -fsSL "$url" -o "$tmp/otelcol.tar.gz"
    tar -xzf "$tmp/otelcol.tar.gz" -C "$tmp"
    install -o root -g root -m 755 "$tmp/otelcol-contrib" "$OTEL_AGENT_BIN"
    rm -rf "$tmp"
}

otel_agent_write_config() {
    local telemetry_host="$1" service_name="$2"
    mkdir -p "$OTEL_AGENT_CONFIG_DIR"
    cat > "$OTEL_AGENT_CONFIG" <<EOF
# Ship ${service_name} journal logs, host metrics, and app OTLP (traces/metrics)
# from localhost → telemetry collector (org=default).
receivers:
  # Local ingress for the Syrinx process (127.0.0.1 only — not exposed on LAN).
  # Traces: gRPC :4317; app metrics: HTTP :4318 (matches observability.Setup).
  otlp:
    protocols:
      grpc:
        endpoint: 127.0.0.1:4317
      http:
        endpoint: 127.0.0.1:4318

  # Zerolog writes JSON to stdout; journald stores that as MESSAGE (a string).
  # Parse it so OpenObserve gets level/message/… as real fields, not one blob.
  journald:
    directory: /var/log/journal
    units:
      - ${service_name}.service
    start_at: end
    operators:
      - type: json_parser
        if: 'body.MESSAGE matches "^{.*}$"'
        parse_from: body.MESSAGE
        parse_to: attributes
        on_error: send
        severity:
          parse_from: attributes.level
          mapping:
            trace: TRACE
            debug: DEBUG
            info: INFO
            warn: WARN
            warning: WARN
            error: ERROR
            fatal: FATAL
            panic: FATAL
      # Replace the journal field map with the log line (drops body___cursor noise).
      - type: move
        from: body.MESSAGE
        to: body

  # Host-level resource usage — aggregate machine stats only, no per-user data.
  hostmetrics:
    collection_interval: 30s
    scrapers:
      cpu:
        metrics:
          system.cpu.utilization:
            enabled: true
      memory:
        metrics:
          system.memory.utilization:
            enabled: true
      disk:
      filesystem:
        metrics:
          system.filesystem.utilization:
            enabled: true
      load:
      network:
      paging:

processors:
  batch:
    timeout: 2s
    send_batch_size: 256
  resource:
    attributes:
      - key: service.name
        value: ${service_name}
        action: upsert
  # Tags host metrics with host.name so multiple app hosts stay distinguishable.
  resourcedetection:
    detectors: [system]
    system:
      hostname_sources: [os]

exporters:
  otlphttp:
    endpoint: http://${telemetry_host}:4318
    tls:
      insecure: true

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [resource, batch]
      exporters: [otlphttp]
    logs:
      receivers: [journald]
      processors: [resource, batch]
      exporters: [otlphttp]
    metrics:
      receivers: [hostmetrics, otlp]
      processors: [resourcedetection, resource, batch]
      exporters: [otlphttp]
EOF
    chmod 644 "$OTEL_AGENT_CONFIG"
}

otel_agent_write_unit() {
    cat > "/etc/systemd/system/$OTEL_AGENT_UNIT" <<EOF
[Unit]
Description=OTLP agent: journald + host metrics + local app ingress (${OTEL_AGENT_UNIT})
After=network-online.target systemd-journald.service
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=${OTEL_AGENT_BIN} --config=${OTEL_AGENT_CONFIG}
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF
}

otel_agent_install() {
    local telemetry_host="$1" service_name="$2"
    if [ -z "$telemetry_host" ] || [ -z "$service_name" ]; then
        echo "❌ otel_agent_install requires <telemetry_host> <service_name>" >&2
        return 1
    fi

    otel_agent_ensure_persistent_journal
    otel_agent_ensure_binary
    otel_agent_write_config "$telemetry_host" "$service_name"
    otel_agent_write_unit
    systemctl daemon-reload
    systemctl enable "$OTEL_AGENT_UNIT" >/dev/null 2>&1 || true
    systemctl restart "$OTEL_AGENT_UNIT"
    sleep 1
    if systemctl is-active --quiet "$OTEL_AGENT_UNIT"; then
        echo "✅ OTLP agent online (logs + host metrics + localhost:4317/:4318 → ${telemetry_host}:4318)"
    else
        echo "❌ otelcol-agent failed to start" >&2
        journalctl -u "$OTEL_AGENT_UNIT" -n 30 --no-pager >&2 || true
        return 1
    fi
}

otel_agent_remove() {
    systemctl disable --now "$OTEL_AGENT_UNIT" 2>/dev/null || true
    rm -f "/etc/systemd/system/$OTEL_AGENT_UNIT"
    rm -rf "$OTEL_AGENT_CONFIG_DIR"
    systemctl daemon-reload 2>/dev/null || true
    echo "✅ OTLP agent removed"
}
