#!/usr/bin/env python3
"""Generate telemetry/dashboards/syrinx-app.dashboard.json (OpenObserve v8)."""
from __future__ import annotations

import json
from pathlib import Path

OUT = Path(__file__).with_name("syrinx-app.dashboard.json")

EMPTY_FILTER = {
    "filterType": "group",
    "logicalOperator": "AND",
    "conditions": [],
}


def axis(label: str, alias: str, *, sort_by: str | None = None) -> dict:
    item = {
        "label": label,
        "alias": alias,
        "column": alias,
        "color": None,
        "aggregationFunction": None,
        "sortBy": sort_by,
        "args": [{"field": alias, "function": None, "alias": alias}],
    }
    return item


def panel_config(*, unit: str | None = None, decimals: float | None = None) -> dict:
    cfg: dict = {
        "show_legends": True,
        "legends_position": None,
        "unit": unit,
        "base_map": {"type": "osm"},
        "map_view": {"zoom": 1.0, "lat": 0.0, "lng": 0.0},
    }
    if decimals is not None:
        cfg["decimals"] = decimals
    return cfg


def sql_panel(
    *,
    pid: str,
    title: str,
    ptype: str,
    sql: str,
    layout: dict,
    x: list[dict],
    y: list[dict],
    breakdown: list[dict] | None = None,
    unit: str | None = None,
    decimals: float | None = None,
    description: str = "",
    stream: str = "syrinx",
    stream_type: str = "traces",
) -> dict:
    return {
        "id": pid,
        "type": ptype,
        "title": title,
        "description": description,
        "config": panel_config(unit=unit, decimals=decimals),
        "queryType": "sql",
        "queries": [
            {
                "query": sql,
                "vrlFunctionQuery": None,
                "customQuery": True,
                "fields": {
                    "stream": stream,
                    "stream_type": stream_type,
                    "x": x,
                    "y": y,
                    "z": [],
                    "breakdown": breakdown or [],
                    "filter": EMPTY_FILTER,
                },
                "config": {
                    "promql_legend": "",
                    "layer_type": "scatter",
                    "weight_fixed": 1.0,
                },
            }
        ],
        "layout": layout,
    }


def promql_panel(
    *,
    pid: str,
    title: str,
    queries: list[tuple[str, str, str]],
    layout: dict,
    unit: str | None = None,
    ptype: str = "line",
    description: str = "",
) -> dict:
    """queries: list of (promql, legend, stream_name)."""
    return {
        "id": pid,
        "type": ptype,
        "title": title,
        "description": description,
        "config": panel_config(unit=unit),
        "queryType": "promql",
        "queries": [
            {
                "query": q,
                "vrlFunctionQuery": None,
                "customQuery": True,
                "fields": {
                    "stream": stream,
                    "stream_type": "metrics",
                    "x": [],
                    "y": [],
                    "z": [],
                    "breakdown": [],
                    "filter": EMPTY_FILTER,
                },
                "config": {
                    "promql_legend": legend,
                    "layer_type": "scatter",
                    "weight_fixed": 1.0,
                },
            }
            for q, legend, stream in queries
        ],
        "layout": layout,
    }


# Bounded counters are charted as events per window. Summing first folds
# every label combination into one line per query.
WINDOW = "5m"


def promql_increase(selector: str, legend: str, stream: str) -> tuple[str, str, str]:
    return (f"sum(increase({selector}[{WINDOW}]))", legend, stream)


def promql_total(metric: str, legend: str) -> tuple[str, str, str]:
    return promql_increase(metric, legend, metric)


def promql_labeled(
    metric: str, attr: str, value: str, legend: str
) -> tuple[str, str, str]:
    return promql_increase(f'{metric}{{{attr}="{value}"}}', legend, metric)


def promql_filter(
    metric: str, labels: dict[str, str], legend: str
) -> tuple[str, str, str]:
    parts = ",".join(f'{k}="{v}"' for k, v in labels.items())
    return promql_increase(f"{metric}{{{parts}}}", legend, metric)


def histogram_avg(metric: str, legend: str) -> tuple[str, str, str]:
    """Mean observation value. OTLP histograms land as separate _sum/_count streams in O2."""
    return (f"{metric}_sum / {metric}_count", legend, f"{metric}_sum")


def histogram_samples(metric: str, legend: str) -> tuple[str, str, str]:
    """Number of histogram recordings in the window (useful for sparse series)."""
    return (f"{metric}_count", legend, f"{metric}_count")


def histogram_quantile_query(
    metric: str, quantile: float, legend: str
) -> tuple[str, str, str]:
    """Percentile band from OTLP histogram buckets over the last WINDOW.
    Empty windows have no observations and render as gaps."""
    return (
        f"histogram_quantile({quantile}, sum by (le) (increase({metric}_bucket[{WINDOW}])))",
        legend,
        f"{metric}_bucket",
    )


def histogram_percentile_bands(
    metric: str, *, p50: str = "p50", p90: str = "p90", p99: str = "p99"
) -> list[tuple[str, str, str]]:
    """p50/p90/p99 bands for an area-chart 'shape of the distribution' panel."""
    return [
        histogram_quantile_query(metric, 0.5, p50),
        histogram_quantile_query(metric, 0.9, p90),
        histogram_quantile_query(metric, 0.99, p99),
    ]


def promql_sum_by_values(
    metric: str, attr: str, values: list[str]
) -> list[tuple[str, str, str]]:
    """One summed line per label value, for counters that carry entity ids.
    increase() would miss each id's first event, so this is the running total
    since the last restart."""
    return [(f'sum({metric}{{{attr}="{v}"}})', v, metric) for v in values]


def top_users_sql(stream: str, labels: list[str], keys: list[str], limit: int = 25) -> str:
    """Top rows by cumulative count. Each series' max(value) is its total;
    a server restart resets it, so totals undercount across restarts."""
    inner = ", ".join(labels)
    outer = ", ".join(keys)
    return (
        f"SELECT {outer}, sum(total) AS total FROM ( "
        f"SELECT {inner}, max(value) AS total FROM {stream} GROUP BY {inner} ) "
        f"WHERE {keys[0]} IS NOT NULL AND {keys[0]} != '' "
        f"GROUP BY {outer} ORDER BY total DESC LIMIT {limit}"
    )


def top_table(*, pid: str, title: str, stream: str, labels: list[str],
              keys: list[tuple[str, str]], layout: dict, description: str,
              limit: int = 25) -> dict:
    return sql_panel(
        pid=pid,
        title=title,
        ptype="table",
        sql=top_users_sql(stream, labels, [k for k, _ in keys], limit),
        layout=layout,
        x=[axis(label, key) for key, label in keys] + [axis("Total", "total")],
        y=[],
        stream=stream,
        stream_type="metrics",
        description=description,
    )


def promql_labeled_queries(
    metric: str, attr: str, pairs: list[tuple[str, str]]
) -> list[tuple[str, str, str]]:
    return [promql_labeled(metric, attr, value, legend) for value, legend in pairs]


def promql_ws_by_type(direction: str) -> list[tuple[str, str, str]]:
    """One line per message type seen, so new types (and PING/PONG) appear
    without listing them here."""
    return [
        (
            f'sum by (ws_message_type) (increase({WS_MESSAGES}{{ws_direction="{direction}"}}[{WINDOW}]))',
            "{ws_message_type}",
            WS_MESSAGES,
        )
    ]


# Grid is 192 units wide (OpenObserve v8 after Host Metrics migration scale).
# Half = 96, quarter = 48, third ≈ 64.


def layout(x: int, y: int, w: int, h: int, i: int) -> dict:
    return {"x": x, "y": y, "w": w, "h": h, "i": i}


def metric_stream(instrument: str) -> str:
    """OTEL name (syrinx.users.created) → OpenObserve metrics stream."""
    return instrument.replace(".", "_")


# syrinx.* business metrics.
USERS_CREATED = metric_stream("syrinx.users.created")
USERS_DELETED = metric_stream("syrinx.users.deleted")
USERS_BACKUP = metric_stream("syrinx.users.backup")
USERS_WITH_IDENTITY = metric_stream("syrinx.users.with_identity_backup")
USERS_WITHOUT_IDENTITY = metric_stream("syrinx.users.without_identity_backup")
USERS_WITH_FULL = metric_stream("syrinx.users.with_full_backup")
USERS_WITHOUT_FULL = metric_stream("syrinx.users.without_full_backup")
KEYS_REVOKED = metric_stream("syrinx.keys.revoked")
REEDS_PUBLISHED = metric_stream("syrinx.reeds.published")
REEDS_DELETED = metric_stream("syrinx.reeds.deleted")
THREADS_PUBLISHED = metric_stream("syrinx.threads.published")
THREAD_SIZE = metric_stream("syrinx.thread.size")
ECHOES_TARGETED = metric_stream("syrinx.echoes.targeted")
CONTENT_REJECTED = metric_stream("syrinx.content.rejected")
REED_HOLDERS = metric_stream("syrinx.reed.holders")
REED_COVERAGE = metric_stream("syrinx.reed.coverage_percent")
WS_MESSAGES = metric_stream("syrinx.ws.messages")
RELAY_EVENTS = metric_stream("syrinx.relay.event")
FEDERATION_RELAY = metric_stream("syrinx.federation.relay")
FEDERATION_BACKLOG = metric_stream("syrinx.federation.delivery.backlog")
FEDERATION_LIFECYCLE = metric_stream("syrinx.federation.lifecycle")
ADMIN_OPENED = metric_stream("syrinx.admin.requests.opened")
ADMIN_REFUSED = metric_stream("syrinx.admin.requests.refused")
ADMIN_DECIDED = metric_stream("syrinx.admin.requests.decided")
ADMIN_DECISION_SECONDS = metric_stream("syrinx.admin.request.decision_seconds")
CRYPTO_OPS = metric_stream("syrinx.crypto.duration_us") + "_count"
ADMIN_EXECUTED = metric_stream("syrinx.admin.actions.executed")
ADMIN_SUSPENDED_REFUSALS = metric_stream("syrinx.admin.suspended_refusals")
ADMIN_MAIL = metric_stream("syrinx.admin.mail.sent")
FLAGS_CREATED = metric_stream("syrinx.flags.created")
FLAGS_DISMISSED = metric_stream("syrinx.flags.dismissed")
LEDGER_APPENDED = metric_stream("syrinx.ledger.appended")
AUTH_REJECTED = metric_stream("syrinx.auth.rejected")
WS_HANDSHAKE_REJECTED = metric_stream("syrinx.ws.handshake.rejected")
WS_CONNECTIONS = metric_stream("syrinx.ws.connections")
SIGNUP_REJECTED = metric_stream("syrinx.signup.rejected")
COOLDOWN_HITS = metric_stream("syrinx.cooldown.hits")
BLOCK_REFUSALS = metric_stream("syrinx.blocks.refusals")
RESERVED_HITS = metric_stream("syrinx.admin.reserved_usernames.hits")
USERNAME_CHECKS = metric_stream("syrinx.username.checks")

ADMIN_ACTIONS = [
    "suspend", "reinstate", "kick", "promote", "demote",
    "permissions", "rebind", "broadcast", "unicast",
]
AUTH_REASONS = [
    "bad_signature", "stale_timestamp", "missing_headers", "bad_scope",
    "key_not_found", "key_revoked", "restricted_key", "account_removed",
    "suspended", "device_mismatch", "not_peer", "server_key_proof",
]
WS_HANDSHAKE_REASONS = [
    "bad_signature", "stale_timestamp", "missing_params", "key_not_found",
    "key_revoked", "not_user_key", "account_removed", "suspended",
    "device_mismatch", "ongoing_import", "internal",
]
SIGNUP_REASONS = [
    "username_taken", "reserved", "invite_required", "invite_invalid",
    "inviter_lacks_grant", "bad_signature", "bad_key", "invalid_request",
    "closed", "recovery_mode",
]

HTTP = "span_kind = '2'"
DB = "db_system = 'postgresql'"
CRYPTO = "operation_name LIKE 'crypto.%'"
PEER_IN = f"{HTTP} AND peer_server_id IS NOT NULL AND peer_server_id != ''"
PEER_OUT = "operation_name = 'federation.out'"
DB_NESTED = (
    f"{DB} AND reference_parent_span_id IS NOT NULL "
    f"AND reference_parent_span_id != ''"
)
DB_ORPHAN = (
    f"{DB} AND (reference_parent_span_id IS NULL "
    f"OR reference_parent_span_id = '')"
)

overview = [
    sql_panel(
        pid="syrinx_stat_reqs",
        title="Requests",
        ptype="metric",
        sql=f"SELECT count(*) AS reqs FROM syrinx WHERE {HTTP}",
        layout=layout(0, 0, 48, 6, 1),
        x=[],
        y=[axis("Requests", "reqs")],
        description="HTTP server spans in the selected window",
    ),
    sql_panel(
        pid="syrinx_stat_err_pct",
        title="Error % (5xx)",
        ptype="metric",
        sql=(
            f"SELECT CASE WHEN count(*) = 0 THEN 0 ELSE "
            f"100.0 * sum(CASE WHEN CAST(http_response_status_code AS BIGINT) >= 500 "
            f"THEN 1 ELSE 0 END) / count(*) END AS err_pct "
            f"FROM syrinx WHERE {HTTP}"
        ),
        layout=layout(48, 0, 48, 6, 2),
        x=[],
        y=[axis("Error %", "err_pct")],
        unit="percent",
        decimals=2,
        description="Share of HTTP server spans with status >= 500",
    ),
    sql_panel(
        pid="syrinx_stat_p95",
        title="p95 latency (ms)",
        ptype="metric",
        sql=(
            f"SELECT approx_percentile_cont(duration, 0.95) / 1000.0 AS p95_ms "
            f"FROM syrinx WHERE {HTTP}"
        ),
        layout=layout(96, 0, 48, 6, 3),
        x=[],
        y=[axis("p95 ms", "p95_ms")],
        decimals=2,
        description="HTTP server span duration; stored µs, shown as ms",
    ),
    sql_panel(
        pid="syrinx_stat_db_spans",
        title="DB spans",
        ptype="metric",
        sql=f"SELECT count(*) AS db_spans FROM syrinx WHERE {DB}",
        layout=layout(144, 0, 48, 6, 4),
        x=[],
        y=[axis("DB spans", "db_spans")],
        description="PostgreSQL client spans in the selected window",
    ),
    sql_panel(
        pid="syrinx_rps",
        title="Request rate",
        ptype="line",
        sql=(
            f"SELECT histogram(_timestamp) AS ts, count(*) AS reqs "
            f"FROM syrinx WHERE {HTTP} GROUP BY ts ORDER BY ts"
        ),
        layout=layout(0, 6, 96, 14, 5),
        x=[axis("Timestamp", "ts", sort_by="ASC")],
        y=[axis("Requests", "reqs")],
    ),
    sql_panel(
        pid="syrinx_errs",
        title="5xx errors",
        ptype="line",
        sql=(
            f"SELECT histogram(_timestamp) AS ts, count(*) AS errs "
            f"FROM syrinx WHERE {HTTP} AND CAST(http_response_status_code AS BIGINT) >= 500 "
            f"GROUP BY ts ORDER BY ts"
        ),
        layout=layout(96, 6, 96, 14, 6),
        x=[axis("Timestamp", "ts", sort_by="ASC")],
        y=[axis("5xx", "errs")],
    ),
    sql_panel(
        pid="syrinx_latency",
        title="Latency p50 / p95 (ms)",
        ptype="line",
        sql=(
            f"SELECT histogram(_timestamp) AS ts, "
            f"approx_percentile_cont(duration, 0.5) / 1000.0 AS p50_ms, "
            f"approx_percentile_cont(duration, 0.95) / 1000.0 AS p95_ms "
            f"FROM syrinx WHERE {HTTP} GROUP BY ts ORDER BY ts"
        ),
        layout=layout(0, 20, 96, 14, 7),
        x=[axis("Timestamp", "ts", sort_by="ASC")],
        y=[axis("p50 ms", "p50_ms"), axis("p95 ms", "p95_ms")],
        decimals=2,
    ),
    promql_panel(
        pid="syrinx_pool_overview",
        title="DB connections (open vs max)",
        queries=[
            ("db_sql_connection_open", "open", "db_sql_connection_open"),
            ("db_sql_connection_max_open", "max_open", "db_sql_connection_max_open"),
        ],
        layout=layout(96, 20, 96, 14, 8),
        description="Connection pool saturation. Host CPU/RAM live on the Host Metrics dashboard.",
    ),
]

# OpenPGP signing and checking: crypto.sign / crypto.verify spans sit under
# the request span, and syrinx.crypto.duration_us counts them.
crypto = [
    promql_panel(
        pid="syrinx_crypto_ops",
        title="Signatures and checks per 5m",
        queries=[
            promql_filter(CRYPTO_OPS, {"crypto_op": "sign"}, "sign"),
            promql_filter(CRYPTO_OPS, {"crypto_op": "verify", "ok": "true"}, "verify ok"),
            promql_filter(CRYPTO_OPS, {"crypto_op": "verify", "ok": "false"}, "verify failed"),
        ],
        layout=layout(0, 0, 96, 14, 89),
        description="Every OpenPGP sign and verify the server performs, incl. response signing",
    ),
    sql_panel(
        pid="syrinx_crypto_latency",
        title="Sign / verify latency p95 (µs)",
        ptype="line",
        sql=(
            f"SELECT histogram(_timestamp) AS ts, operation_name, "
            f"approx_percentile_cont(duration, 0.95) AS p95_us "
            f"FROM syrinx WHERE {CRYPTO} GROUP BY ts, operation_name ORDER BY ts"
        ),
        layout=layout(96, 0, 96, 14, 90),
        x=[axis("Timestamp", "ts", sort_by="ASC")],
        y=[axis("p95 µs", "p95_us")],
        breakdown=[axis("Operation", "operation_name")],
        description="From crypto spans; an Ed25519 signature is ~25 µs, RSA-2048 ~1 ms",
    ),
    sql_panel(
        pid="syrinx_crypto_per_request",
        title="Signatures and checks per request, by route",
        ptype="table",
        sql=(
            f"SELECT r.http_route AS route, count(DISTINCT r.trace_id) AS reqs, "
            f"sum(CASE WHEN c.operation_name = 'crypto.sign' THEN 1 ELSE 0 END) * 1.0 "
            f"/ count(DISTINCT r.trace_id) AS signs_per_req, "
            f"sum(CASE WHEN c.operation_name = 'crypto.verify' THEN 1 ELSE 0 END) * 1.0 "
            f"/ count(DISTINCT r.trace_id) AS verifies_per_req, "
            f"sum(c.duration) * 1.0 / count(DISTINCT r.trace_id) AS crypto_us_per_req "
            f"FROM syrinx r JOIN syrinx c ON c.trace_id = r.trace_id "
            f"WHERE r.{HTTP} AND r.http_route IS NOT NULL AND c.{CRYPTO} "
            f"GROUP BY route ORDER BY crypto_us_per_req DESC LIMIT 30"
        ),
        layout=layout(0, 14, 192, 18, 91),
        x=[axis("Route", "route")],
        y=[
            axis("Requests", "reqs"),
            axis("Signs / req", "signs_per_req"),
            axis("Verifies / req", "verifies_per_req"),
            axis("Crypto µs / req", "crypto_us_per_req"),
        ],
        decimals=2,
        description="Requests that did any crypto, joined to their crypto spans by trace",
    ),
    sql_panel(
        pid="syrinx_crypto_algorithms",
        title="Operations by key algorithm",
        ptype="bar",
        sql=(
            f"SELECT crypto_algorithm, operation_name, count(*) AS cnt FROM syrinx "
            f"WHERE {CRYPTO} GROUP BY crypto_algorithm, operation_name ORDER BY cnt DESC"
        ),
        layout=layout(0, 32, 192, 12, 92),
        x=[axis("Algorithm", "crypto_algorithm")],
        y=[axis("Count", "cnt")],
        breakdown=[axis("Operation", "operation_name")],
        description="eddsa for current server keys; rsa only from keys made before the switch",
    ),
]

# Traffic exchanged with peer servers, foremost what peers cost this one:
# request spans carry peer.server_id once a peer is authenticated, and
# every call out is a federation.out span.
federation = [
    sql_panel(
        pid="syrinx_fed_in_volume",
        title="Requests from peers, by peer",
        ptype="line",
        sql=(
            f"SELECT histogram(_timestamp) AS ts, peer_server_id, count(*) AS reqs "
            f"FROM syrinx WHERE {PEER_IN} GROUP BY ts, peer_server_id ORDER BY ts"
        ),
        layout=layout(0, 0, 96, 14, 94),
        x=[axis("Timestamp", "ts", sort_by="ASC")],
        y=[axis("Requests", "reqs")],
        breakdown=[axis("Peer", "peer_server_id")],
        description="Authenticated peer requests handled here",
    ),
    sql_panel(
        pid="syrinx_fed_out_volume",
        title="Calls to peers, by peer",
        ptype="line",
        sql=(
            f"SELECT histogram(_timestamp) AS ts, peer_server_id, count(*) AS calls "
            f"FROM syrinx WHERE {PEER_OUT} GROUP BY ts, peer_server_id ORDER BY ts"
        ),
        layout=layout(96, 0, 96, 14, 95),
        x=[axis("Timestamp", "ts", sort_by="ASC")],
        y=[axis("Calls", "calls")],
        breakdown=[axis("Peer", "peer_server_id")],
        description="Every server-to-server call this server makes",
    ),
    sql_panel(
        pid="syrinx_fed_in_cost",
        title="What peers ask of us",
        ptype="table",
        sql=(
            f"SELECT r.peer_server_id AS peer, r.http_route AS route, "
            f"count(DISTINCT r.span_id) AS reqs, "
            f"count(DISTINCT CASE WHEN r.http_response_status_code >= 400 "
            f"THEN r.span_id END) AS errors, "
            f"approx_percentile_cont(r.duration, 0.95) / 1000.0 AS p95_ms, "
            f"sum(CASE WHEN q.operation_name IN ('sql.conn.query', 'sql.conn.exec') "
            f"THEN 1 ELSE 0 END) * 1.0 / count(DISTINCT r.span_id) AS stmts_per_req, "
            f"sum(CASE WHEN q.{CRYPTO} THEN 1 ELSE 0 END) * 1.0 "
            f"/ count(DISTINCT r.span_id) AS crypto_per_req "
            f"FROM syrinx r LEFT JOIN syrinx q ON q.trace_id = r.trace_id "
            f"AND q.span_id != r.span_id "
            f"WHERE r.{HTTP} AND r.peer_server_id IS NOT NULL AND r.peer_server_id != '' "
            f"GROUP BY peer, route ORDER BY reqs DESC LIMIT 40"
        ),
        layout=layout(0, 14, 192, 18, 96),
        x=[axis("Peer", "peer"), axis("Route", "route")],
        y=[
            axis("Requests", "reqs"),
            axis("Errors", "errors"),
            axis("p95 ms", "p95_ms"),
            axis("DB statements / req", "stmts_per_req"),
            axis("Sign+verify / req", "crypto_per_req"),
        ],
        decimals=2,
        description="Per peer and route: volume, failures, latency and the DB and crypto work each request costs here",
    ),
    sql_panel(
        pid="syrinx_fed_out_cost",
        title="Our calls to peers",
        ptype="table",
        sql=(
            f"SELECT peer_server_id AS peer, federation_leg AS leg, count(*) AS calls, "
            f"sum(CASE WHEN span_status = 'ERROR' THEN 1 ELSE 0 END) AS failures, "
            f"approx_percentile_cont(duration, 0.95) / 1000.0 AS p95_ms "
            f"FROM syrinx WHERE {PEER_OUT} GROUP BY peer, leg ORDER BY calls DESC LIMIT 40"
        ),
        layout=layout(0, 32, 192, 16, 97),
        x=[axis("Peer", "peer"), axis("Leg", "leg")],
        y=[axis("Calls", "calls"), axis("Failures", "failures"), axis("p95 ms", "p95_ms")],
        decimals=2,
        description="Failures: transport errors and 4xx/5xx answers",
    ),
    promql_panel(
        pid="syrinx_fed_backlog",
        title="Delivery backlog by peer",
        queries=[
            (f"sum by (peer_server_id) ({FEDERATION_BACKLOG})", "{peer_server_id}", FEDERATION_BACKLOG),
        ],
        layout=layout(0, 48, 96, 14, 98),
        description="Author streams still owed to each peer",
    ),
    promql_panel(
        pid="syrinx_fed_lifecycle",
        title="Peer connects and disconnects",
        queries=[
            (
                f"sum by (event) (increase({FEDERATION_LIFECYCLE}[{WINDOW}]))",
                "{event}",
                FEDERATION_LIFECYCLE,
            ),
        ],
        layout=layout(96, 48, 96, 14, 99),
    ),
]

requests = [
    sql_panel(
        pid="syrinx_top_routes",
        title="Top routes by volume",
        ptype="bar",
        sql=(
            f"SELECT http_route, count(*) AS reqs FROM syrinx "
            f"WHERE {HTTP} AND http_route IS NOT NULL "
            f"GROUP BY http_route ORDER BY reqs DESC LIMIT 20"
        ),
        layout=layout(0, 0, 96, 14, 10),
        x=[axis("Route", "http_route")],
        y=[axis("Requests", "reqs")],
    ),
    sql_panel(
        pid="syrinx_slow_routes",
        title="Slowest routes (p95 ms)",
        ptype="table",
        sql=(
            f"SELECT http_route, "
            f"approx_percentile_cont(duration, 0.95) / 1000.0 AS p95_ms, "
            f"count(*) AS reqs FROM syrinx "
            f"WHERE {HTTP} AND http_route IS NOT NULL AND http_route != '/ws/' "
            f"GROUP BY http_route ORDER BY p95_ms DESC LIMIT 20"
        ),
        layout=layout(96, 0, 96, 14, 11),
        x=[
            axis("Route", "http_route"),
            axis("p95 ms", "p95_ms"),
            axis("Requests", "reqs"),
        ],
        y=[],
        decimals=2,
    ),
    sql_panel(
        pid="syrinx_status",
        title="Status codes over time",
        ptype="stacked",
        sql=(
            f"SELECT histogram(_timestamp) AS ts, http_response_status_code, count(*) AS cnt "
            f"FROM syrinx WHERE {HTTP} "
            f"GROUP BY ts, http_response_status_code ORDER BY ts"
        ),
        layout=layout(0, 14, 192, 14, 12),
        x=[axis("Timestamp", "ts", sort_by="ASC")],
        y=[axis("Count", "cnt")],
        breakdown=[axis("Status", "http_response_status_code")],
    ),
    sql_panel(
        pid="syrinx_route_latency_table",
        title="Route latency summary",
        ptype="table",
        sql=(
            f"SELECT http_route, count(*) AS reqs, "
            f"approx_percentile_cont(duration, 0.5) / 1000.0 AS p50_ms, "
            f"approx_percentile_cont(duration, 0.95) / 1000.0 AS p95_ms, "
            f"avg(duration) / 1000.0 AS avg_ms "
            f"FROM syrinx WHERE {HTTP} AND http_route IS NOT NULL AND http_route != '/ws/' "
            f"GROUP BY http_route ORDER BY reqs DESC LIMIT 30"
        ),
        layout=layout(0, 28, 192, 16, 13),
        x=[
            axis("Route", "http_route"),
            axis("Requests", "reqs"),
            axis("p50 ms", "p50_ms"),
            axis("p95 ms", "p95_ms"),
            axis("avg ms", "avg_ms"),
        ],
        y=[],
        decimals=2,
        description="Outliers without a heatmap — sort by p95 in the UI if needed",
    ),
]

database = [
    promql_panel(
        pid="syrinx_pool_open_wait",
        title="Pool open / wait",
        queries=[
            ("db_sql_connection_open", "open", "db_sql_connection_open"),
            ("db_sql_connection_wait", "wait", "db_sql_connection_wait"),
        ],
        layout=layout(0, 0, 96, 14, 20),
    ),
    promql_panel(
        pid="syrinx_pool_wait_duration",
        title="Pool wait duration",
        queries=[
            (
                "db_sql_connection_wait_duration",
                "wait_duration",
                "db_sql_connection_wait_duration",
            )
        ],
        layout=layout(96, 0, 96, 14, 21),
        unit="seconds",
        description="Time spent waiting for a free connection",
    ),
    sql_panel(
        pid="syrinx_db_lat",
        title="DB query latency p95 (ms)",
        ptype="line",
        sql=(
            f"SELECT histogram(_timestamp) AS ts, "
            f"approx_percentile_cont(duration, 0.95) / 1000.0 AS p95_ms "
            f"FROM syrinx WHERE {DB} GROUP BY ts ORDER BY ts"
        ),
        layout=layout(0, 14, 96, 14, 22),
        x=[axis("Timestamp", "ts", sort_by="ASC")],
        y=[axis("p95 ms", "p95_ms")],
        decimals=2,
        description="From DB spans (preferred over empty histogram_quantile buckets)",
    ),
    sql_panel(
        pid="syrinx_db_ops",
        title="DB ops by type",
        ptype="bar",
        sql=(
            f"SELECT operation_name, count(*) AS cnt FROM syrinx "
            f"WHERE {DB} GROUP BY operation_name ORDER BY cnt DESC"
        ),
        layout=layout(96, 14, 96, 14, 23),
        x=[axis("Operation", "operation_name")],
        y=[axis("Count", "cnt")],
    ),
    sql_panel(
        pid="syrinx_slow_queries",
        title="Slowest queries (p95 ms)",
        ptype="table",
        sql=(
            f"SELECT substr(db_query_text, 1, 120) AS query, count(*) AS cnt, "
            f"approx_percentile_cont(duration, 0.95) / 1000.0 AS p95_ms, "
            f"avg(duration) / 1000.0 AS avg_ms "
            f"FROM syrinx WHERE {DB} AND db_query_text IS NOT NULL AND db_query_text != '' "
            f"GROUP BY query ORDER BY p95_ms DESC LIMIT 20"
        ),
        layout=layout(0, 28, 192, 18, 24),
        x=[
            axis("Query", "query"),
            axis("Count", "cnt"),
            axis("p95 ms", "p95_ms"),
            axis("avg ms", "avg_ms"),
        ],
        y=[],
        decimals=2,
        description="Statement text without bind args; truncated to 120 chars",
    ),
    sql_panel(
        pid="syrinx_db_nesting",
        title="DB spans: nested vs orphan",
        ptype="bar",
        sql=(
            f"SELECT nesting, count(*) AS cnt FROM ( "
            f"SELECT CASE WHEN reference_parent_span_id IS NOT NULL "
            f"AND reference_parent_span_id != '' THEN 'nested under request' "
            f"ELSE 'orphan (no parent span)' END AS nesting "
            f"FROM syrinx WHERE {DB} ) GROUP BY nesting ORDER BY cnt DESC"
        ),
        layout=layout(0, 46, 64, 14, 25),
        x=[axis("Nesting", "nesting")],
        y=[axis("Spans", "cnt")],
        description=(
            "After context threading, DB spans should nest under the HTTP request "
            "span (reference_parent_span_id set). Orphans indicate unmigrated call sites."
        ),
    ),
    sql_panel(
        pid="syrinx_db_nested_pct",
        title="Nested DB span % over time",
        ptype="line",
        sql=(
            f"SELECT histogram(_timestamp) AS ts, "
            f"100.0 * sum(CASE WHEN reference_parent_span_id IS NOT NULL "
            f"AND reference_parent_span_id != '' THEN 1 ELSE 0 END) / count(*) "
            f"AS nested_pct FROM syrinx WHERE {DB} GROUP BY ts ORDER BY ts"
        ),
        layout=layout(64, 46, 64, 14, 26),
        x=[axis("Timestamp", "ts", sort_by="ASC")],
        y=[axis("Nested %", "nested_pct")],
        unit="percent",
        decimals=1,
        description="Share of PostgreSQL client spans with a parent request span",
    ),
    sql_panel(
        pid="syrinx_db_queries_per_route",
        title="DB queries per request by route",
        ptype="table",
        sql=(
            f"SELECT http_route, avg(db_queries) AS avg_queries, "
            f"approx_percentile_cont(db_queries, 0.95) AS p95_queries, "
            f"count(*) AS requests FROM ( "
            f"SELECT h.http_route, h.trace_id, count(d.span_id) AS db_queries "
            f"FROM syrinx h INNER JOIN syrinx d ON h.trace_id = d.trace_id "
            f"AND d.db_system = 'postgresql' "
            f"WHERE {HTTP} AND h.http_route IS NOT NULL "
            f"GROUP BY h.http_route, h.trace_id ) "
            f"GROUP BY http_route ORDER BY avg_queries DESC LIMIT 25"
        ),
        layout=layout(128, 46, 64, 14, 27),
        x=[
            axis("Route", "http_route"),
            axis("Avg queries", "avg_queries"),
            axis("p95 queries", "p95_queries"),
            axis("Requests", "requests"),
        ],
        y=[],
        decimals=2,
        description=(
            "Per HTTP request trace: count of nested DB spans, averaged by route. "
            "Requires context-threaded queries (trace_id join)."
        ),
    ),
    sql_panel(
        pid="syrinx_db_time_share",
        title="DB time as % of request duration",
        ptype="line",
        sql=(
            f"SELECT histogram(_timestamp) AS ts, "
            f"avg(db_ms * 100.0 / nullif(http_ms, 0)) AS db_pct FROM ( "
            f"SELECT h._timestamp, h.duration / 1000.0 AS http_ms, "
            f"coalesce(sum(d.duration), 0) / 1000.0 AS db_ms "
            f"FROM syrinx h LEFT JOIN syrinx d ON h.trace_id = d.trace_id "
            f"AND d.db_system = 'postgresql' WHERE {HTTP} "
            f"GROUP BY h.trace_id, h._timestamp, h.duration ) "
            f"GROUP BY ts ORDER BY ts"
        ),
        layout=layout(0, 60, 192, 14, 28),
        x=[axis("Timestamp", "ts", sort_by="ASC")],
        y=[axis("DB % of request", "db_pct")],
        unit="percent",
        decimals=1,
        description=(
            "Sum of nested DB span durations divided by HTTP span duration, "
            "averaged per trace then over time"
        ),
    ),
]

users = [
    promql_panel(
        pid="syrinx_users_created",
        title="Signups",
        queries=[
            promql_total(USERS_CREATED, "total"),
            promql_labeled(USERS_CREATED, "signup_mode", "open", "open"),
            promql_labeled(USERS_CREATED, "signup_mode", "invite", "invite"),
            promql_labeled(USERS_CREATED, "signup_mode", "closed", "closed"),
        ],
        layout=layout(0, 0, 96, 14, 30),
        description="Successful signups by signup.mode (open / invite / closed)",
    ),
    promql_panel(
        pid="syrinx_users_deleted",
        title="Account deletions",
        queries=[
            promql_total(USERS_DELETED, "total"),
            promql_labeled(USERS_DELETED, "note_has", "true", "with note"),
            promql_labeled(USERS_DELETED, "note_has", "false", "silent"),
        ],
        layout=layout(96, 0, 96, 14, 31),
        description=(
            "Account removals on first successful cert (not replays). "
            "note.has = whether a goodbye note was supplied (never the text)."
        ),
    ),
    promql_panel(
        pid="syrinx_keys_revoked",
        title="Key revocations",
        queries=[promql_total(KEYS_REVOKED, "revoked")],
        layout=layout(0, 14, 96, 14, 32),
        description="Successful key revocations, rotations and rebinds together",
    ),
    promql_panel(
        pid="syrinx_user_churn",
        title="Signups vs deletions",
        queries=[
            promql_total(USERS_CREATED, "created"),
            promql_total(USERS_DELETED, "deleted"),
        ],
        layout=layout(96, 14, 96, 14, 33),
        description="Compare signup and account-deletion counters over the selected window",
    ),
    promql_panel(
        pid="syrinx_users_identity_backup",
        title="Users with identity backup (.sxi)",
        queries=[
            (USERS_WITH_IDENTITY, "backed up", USERS_WITH_IDENTITY),
            (USERS_WITHOUT_IDENTITY, "never backed up", USERS_WITHOUT_IDENTITY),
        ],
        layout=layout(0, 28, 96, 14, 34),
        ptype="bar",
        description=(
            "Live snapshot from DB counters (identity_backup_count > 0). "
            "Updated every metrics scrape (~10s)."
        ),
    ),
    promql_panel(
        pid="syrinx_users_full_backup",
        title="Users with full backup (.sxb)",
        queries=[
            (USERS_WITH_FULL, "backed up", USERS_WITH_FULL),
            (USERS_WITHOUT_FULL, "never backed up", USERS_WITHOUT_FULL),
        ],
        layout=layout(96, 28, 96, 14, 35),
        ptype="bar",
        description=(
            "Live snapshot from DB counters (full_backup_count > 0). "
            "Separate from keys-only identity export."
        ),
    ),
    promql_panel(
        pid="syrinx_users_backup_events",
        title="Backup events by kind",
        queries=[
            promql_total(USERS_BACKUP, "total"),
            promql_labeled(USERS_BACKUP, "backup_kind", "identity", "identity (.sxi)"),
            promql_labeled(USERS_BACKUP, "backup_kind", "full", "full (.sxb)"),
        ],
        layout=layout(0, 42, 96, 14, 36),
        description=(
            "Successful SPA export acknowledgements via POST /users/me/backup "
            "(after the file is saved locally)"
        ),
    ),
    promql_panel(
        pid="syrinx_users_backup_repeat",
        title="First vs repeat backups",
        queries=[
            promql_labeled(USERS_BACKUP, "backup_repeat", "false", "first time"),
            promql_labeled(USERS_BACKUP, "backup_repeat", "true", "repeat"),
        ],
        layout=layout(96, 42, 96, 14, 37),
        description=(
            "repeat=true when the user had already recorded at least one backup "
            "of the same kind (identity or full)"
        ),
    ),
]

reeds = [
    promql_panel(
        pid="syrinx_reeds_published",
        title="Publishes by kind",
        queries=[
            promql_total(REEDS_PUBLISHED, "total"),
            *promql_labeled_queries(
                REEDS_PUBLISHED,
                "reed_kind",
                [
                    ("plain", "plain"),
                    ("echo", "echo"),
                    ("reply", "reply"),
                    ("thread", "thread part"),
                ],
            ),
        ],
        layout=layout(0, 0, 96, 14, 40),
        description="Successful SignReed creates (not replays), split by reed.kind",
    ),
    promql_panel(
        pid="syrinx_echoes_targeted",
        title="Echoes published",
        queries=[
            promql_total(ECHOES_TARGETED, "chorus echoes"),
            (
                f'sum(increase({REEDS_PUBLISHED}{{reed_kind="echo"}}[{WINDOW}])) '
                f"- sum(increase({ECHOES_TARGETED}[{WINDOW}]))",
                "self echoes",
                REEDS_PUBLISHED,
            ),
        ],
        layout=layout(96, 0, 96, 14, 41),
        description=(
            "Chorus echoes count towards the original reed; self echoes are users "
            "echoing their own reed, which don't"
        ),
    ),
    promql_panel(
        pid="syrinx_reeds_deleted",
        title="Reed deletions",
        queries=[promql_total(REEDS_DELETED, "deleted")],
        layout=layout(0, 14, 48, 14, 42),
        description=(
            "Successful reed removal certs (not replays). "
            "No Data = no deletions in the selected window."
        ),
    ),
    promql_panel(
        pid="syrinx_reeds_rejected",
        title="Rejected reeds by reason",
        queries=[
            (
                f'sum by (reason) (increase({CONTENT_REJECTED}{{store_name=~"reeds|threads"}}[{WINDOW}]))',
                "{reason}",
                CONTENT_REJECTED,
            ),
        ],
        layout=layout(48, 14, 48, 14, 43),
        description=(
            "Reeds and threads clients refused to store: bad signatures, tampering, "
            "over-limit content, unknown or revoked keys, false tag claims"
        ),
    ),
    promql_panel(
        pid="syrinx_reeds_tags",
        title="Publishes with tags",
        queries=[
            promql_labeled(REEDS_PUBLISHED, "tags_has", "true", "with tags"),
            promql_labeled(REEDS_PUBLISHED, "tags_has", "false", "no tags"),
            promql_labeled(REEDS_PUBLISHED, "tags_count", "4", "4+ tags"),
        ],
        layout=layout(96, 14, 96, 14, 44),
        description="Tag count bucketed 0–3 exact, 4 = four or more (never tag text)",
    ),
    promql_panel(
        pid="syrinx_reed_holders_coverage",
        title="Reed holders & coverage % — p50/p90/p99",
        ptype="area",
        queries=[
            *histogram_percentile_bands(
                REED_HOLDERS, p50="holders p50", p90="holders p90", p99="holders p99"
            ),
            histogram_quantile_query(REED_COVERAGE, 0.5, "coverage % p50"),
        ],
        layout=layout(0, 28, 192, 14, 47),
        description=(
            "holders and coverage % are the same event (ReedCoverage records both "
            "together — coverage % = holders / active users) so they're merged here. "
            "Shape from OTLP histogram buckets instead of a flattened mean."
        ),
    ),
    promql_panel(
        pid="syrinx_threads_published",
        title="Threads published",
        queries=[
            promql_total(THREADS_PUBLISHED, "threads"),
            promql_labeled(REEDS_PUBLISHED, "reed_kind", "thread", "thread parts"),
        ],
        layout=layout(0, 42, 96, 14, 48),
        description=(
            "Successful CreateThread calls (not replays), next to the reeds they "
            "contain (syrinx.reeds.published with reed.kind=thread)."
        ),
    ),
    promql_panel(
        pid="syrinx_thread_size",
        title="Thread size (parts) — p50/p90/p99",
        ptype="area",
        queries=histogram_percentile_bands(THREAD_SIZE),
        layout=layout(96, 42, 96, 14, 49),
        description=(
            "Parts per thread at publish, from OTLP histogram buckets. "
            "Threads cap at 30 parts."
        ),
    ),
]

websocket = [
    promql_panel(
        pid="syrinx_ws_volume",
        title="WebSocket messages",
        queries=[
            promql_total(WS_MESSAGES, "total"),
            promql_labeled(WS_MESSAGES, "ws_direction", "in", "in"),
            promql_labeled(WS_MESSAGES, "ws_direction", "out", "out"),
        ],
        layout=layout(0, 0, 96, 14, 50),
        description="Every handled WS frame, by normalized message type",
    ),
    promql_panel(
        pid="syrinx_ws_in_types",
        title="Inbound by message type",
        queries=promql_ws_by_type("in"),
        layout=layout(96, 0, 96, 14, 51),
        description="Client → server frames (ws.direction=in)",
    ),
    promql_panel(
        pid="syrinx_ws_out_types",
        title="Outbound by message type",
        queries=promql_ws_by_type("out"),
        layout=layout(0, 14, 192, 14, 52),
        description="Server → client frames (ws.direction=out)",
    ),
    sql_panel(
        pid="syrinx_ws_work",
        title="Work per WebSocket message type",
        ptype="table",
        sql=(
            f"SELECT w.operation_name AS work, count(DISTINCT w.trace_id) AS msgs, "
            f"approx_percentile_cont(w.duration, 0.95) / 1000.0 AS p95_ms, "
            f"sum(CASE WHEN q.operation_name IN ('sql.conn.query', 'sql.conn.exec') "
            f"THEN 1 ELSE 0 END) * 1.0 / count(DISTINCT w.trace_id) AS stmts_per_msg, "
            f"sum(CASE WHEN q.{CRYPTO} THEN 1 ELSE 0 END) * 1.0 "
            f"/ count(DISTINCT w.trace_id) AS crypto_per_msg "
            f"FROM syrinx w LEFT JOIN syrinx q ON q.trace_id = w.trace_id "
            f"AND q.span_id != w.span_id "
            f"WHERE (w.operation_name LIKE 'ws.%' OR w.operation_name LIKE 'presence.%') "
            f"AND (w.reference_parent_span_id IS NULL OR w.reference_parent_span_id = '') "
            f"GROUP BY work ORDER BY stmts_per_msg * msgs DESC LIMIT 40"
        ),
        layout=layout(0, 28, 192, 18, 93),
        x=[axis("Work", "work")],
        y=[
            axis("Count", "msgs"),
            axis("p95 ms", "p95_ms"),
            axis("DB statements / msg", "stmts_per_msg"),
            axis("Sign+verify / msg", "crypto_per_msg"),
        ],
        decimals=2,
        description="Each inbound message, connect, disconnect, relay timeout and presence reap is its own trace",
    ),
    promql_panel(
        pid="syrinx_ws_relay_health",
        title="Relay path (in vs out)",
        queries=[
            promql_filter(
                WS_MESSAGES,
                {"ws_direction": "in", "ws_message_type": "REQUEST_REED"},
                "in: REQUEST_REED",
            ),
            promql_filter(
                WS_MESSAGES,
                {"ws_direction": "in", "ws_message_type": "RELAY_RESPONSE"},
                "in: RELAY_RESPONSE",
            ),
            promql_filter(
                WS_MESSAGES,
                {"ws_direction": "in", "ws_message_type": "RELAY_MISS"},
                "in: RELAY_MISS",
            ),
            promql_filter(
                WS_MESSAGES,
                {"ws_direction": "out", "ws_message_type": "RELAY_REQUEST"},
                "out: RELAY_REQUEST",
            ),
            promql_filter(
                WS_MESSAGES,
                {"ws_direction": "out", "ws_message_type": "DATA_RESPONSE"},
                "out: DATA_RESPONSE",
            ),
        ],
        layout=layout(0, 46, 192, 14, 53),
        description="Quick view of reed relay request/response/miss balance",
    ),
]

relay = [
    promql_panel(
        pid="syrinx_relay_lifecycle",
        title="Pending relay events by lifecycle stage",
        queries=[
            promql_labeled(RELAY_EVENTS, "relay_lifecycle", "created", "created"),
            promql_labeled(RELAY_EVENTS, "relay_lifecycle", "fulfilled", "fulfilled"),
            promql_labeled(RELAY_EVENTS, "relay_lifecycle", "deleted", "deleted"),
        ],
        layout=layout(0, 0, 96, 14, 60),
        description=(
            "pending_events rows created, fulfilled and deleted per 5m "
            "(syrinx.relay.event). Created persistently above fulfilled plus "
            "deleted means requests are leaking."
        ),
    ),
    promql_panel(
        pid="syrinx_relay_lifecycle_by_kind",
        title="Relay events by kind",
        queries=[
            *promql_labeled_queries(
                RELAY_EVENTS,
                "event_kind",
                [
                    ("request_reed", "request_reed"),
                    ("profile_subscription", "profile_subscription"),
                    ("follow_reed", "follow_reed"),
                    ("broadcast_reed", "broadcast_reed"),
                    ("pipe_reed", "pipe_reed"),
                    ("reed_removed", "reed_removed"),
                    ("account_removed", "account_removed"),
                    ("reed_reply", "reed_reply"),
                ],
            ),
        ],
        layout=layout(96, 0, 96, 14, 61),
        description="All lifecycle stages summed together, split by event.kind",
    ),
    promql_panel(
        pid="syrinx_federation_relay_volume",
        title="Federation relay traffic",
        queries=[
            promql_total(FEDERATION_RELAY, "total"),
            promql_labeled(FEDERATION_RELAY, "federation_direction", "in", "in (from peers)"),
            promql_labeled(FEDERATION_RELAY, "federation_direction", "out", "out (to peers)"),
        ],
        layout=layout(0, 14, 96, 14, 64),
        description="Every federation relay leg handled or called, either direction",
    ),
    promql_panel(
        pid="syrinx_federation_relay_errors",
        title="Federation relay error rate",
        queries=[
            promql_labeled(FEDERATION_RELAY, "ok", "false", "errors"),
        ],
        layout=layout(96, 14, 96, 14, 65),
        description="ok=false: non-2xx response either handling or calling a peer leg",
    ),
    top_table(
        pid="syrinx_federation_relay_by_peer",
        title="Relay traffic by peer server",
        stream=FEDERATION_RELAY,
        labels=["peer_server_id", "federation_direction", "federation_leg", "ok"],
        keys=[("peer_server_id", "Peer server"), ("federation_direction", "Direction"),
              ("federation_leg", "Leg")],
        layout=layout(0, 28, 192, 16, 66),
        description=(
            "How much relay traffic each foreign server is generating (in) or "
            "receiving from us (out), broken down by leg. peer.server_id is "
            "this server's own DB id for the peer, not a raw domain/URL."
        ),
    ),
]

admin = [
    promql_panel(
        pid="syrinx_admin_opened",
        title="Admin requests opened by action",
        queries=promql_sum_by_values(ADMIN_OPENED, "action", ADMIN_ACTIONS),
        layout=layout(0, 0, 96, 14, 67),
        description="Requests an admin signed and the server stored, awaiting a second admin",
    ),
    promql_panel(
        pid="syrinx_admin_decided",
        title="Admin decisions",
        queries=promql_sum_by_values(ADMIN_DECIDED, "decision", ["approve", "reject", "withdraw"]),
        layout=layout(96, 0, 96, 14, 68),
        description="Requests closed by approval, rejection, or the requester's withdrawal",
    ),
    promql_panel(
        pid="syrinx_admin_executed",
        title="Admin actions executed",
        queries=[
            *promql_sum_by_values(ADMIN_EXECUTED, "action", ADMIN_ACTIONS),
            (f'sum({ADMIN_EXECUTED}{{ok="false"}})', "failed", ADMIN_EXECUTED),
        ],
        layout=layout(0, 14, 96, 14, 69),
        description="Approved actions, counted once per target (once for a broadcast)",
    ),
    promql_panel(
        pid="syrinx_admin_refused",
        title="Admin refusals by stage",
        queries=promql_sum_by_values(ADMIN_REFUSED, "stage", ["open", "decide"]),
        layout=layout(96, 14, 96, 14, 70),
        description=(
            "Requests or decisions the admin rules refused. A burst from one "
            "admin is the signal that their key is being misused; see the table below."
        ),
    ),
    top_table(
        pid="syrinx_admin_refused_by_actor",
        title="Admin refusals by admin and reason",
        stream=ADMIN_REFUSED,
        labels=["actor_id", "action", "stage", "reason"],
        keys=[("actor_id", "Admin"), ("action", "Action"), ("reason", "Reason")],
        layout=layout(0, 28, 96, 16, 71),
        description="Who keeps hitting admin refusals, and which rule stopped them",
    ),
    top_table(
        pid="syrinx_admin_targets",
        title="Admin actions by target",
        stream=ADMIN_EXECUTED,
        labels=["target_id", "action", "requester_id", "approver_id", "ok"],
        keys=[("target_id", "Target"), ("action", "Action"),
              ("requester_id", "Requested by"), ("approver_id", "Approved by")],
        layout=layout(96, 28, 96, 16, 72),
        description="Who was suspended, kicked, rebound or messaged, and by which two admins",
    ),
    promql_panel(
        pid="syrinx_admin_decision_time",
        title="Time to decision (seconds)",
        queries=histogram_percentile_bands(ADMIN_DECISION_SECONDS),
        layout=layout(0, 44, 96, 14, 73),
        ptype="area",
        description="Open-to-decision time for approvals and rejections",
    ),
    promql_panel(
        pid="syrinx_admin_suspended_refusals",
        title="Suspended users refused",
        queries=promql_sum_by_values(ADMIN_SUSPENDED_REFUSALS, "surface", ["http", "ws"]),
        layout=layout(96, 44, 96, 14, 74),
        description="Requests and WebSocket handshakes from suspended accounts",
    ),
    promql_panel(
        pid="syrinx_flags",
        title="Flags raised vs dismissed",
        queries=[
            *promql_sum_by_values(FLAGS_CREATED, "origin", ["local", "peer"]),
            (f"sum({FLAGS_DISMISSED})", "dismissals", FLAGS_DISMISSED),
        ],
        layout=layout(0, 58, 96, 14, 75),
        description="Flags from this server's members and from peers', and admin dismissals",
    ),
    top_table(
        pid="syrinx_flags_by_author",
        title="Most flagged authors",
        stream=FLAGS_CREATED,
        labels=["author_id", "reed_id", "reporter_id", "origin", "note_has"],
        keys=[("author_id", "Author")],
        layout=layout(96, 58, 96, 14, 76),
        description="Authors whose reeds draw the most flags",
    ),
    promql_panel(
        pid="syrinx_admin_mail",
        title="Admin mail",
        queries=[
            (f'sum({ADMIN_MAIL}{{action="broadcast"}})', "broadcasts", ADMIN_MAIL),
            (f'sum({ADMIN_MAIL}{{action="unicast"}})', "private messages", ADMIN_MAIL),
        ],
        layout=layout(0, 72, 96, 14, 77),
        description="Approved broadcasts and private messages delivered to mailboxes",
    ),
    promql_panel(
        pid="syrinx_ledger_appended",
        title="Ledger entries",
        queries=[promql_total(LEDGER_APPENDED, "entries")],
        layout=layout(96, 72, 96, 14, 78),
        description="Committed ledger entries; split by entry.kind in Explore",
    ),
]

abuse = [
    promql_panel(
        pid="syrinx_auth_rejected",
        title="Auth rejections by reason",
        queries=promql_sum_by_values(AUTH_REJECTED, "reason", AUTH_REASONS),
        layout=layout(0, 0, 96, 14, 79),
        description="HTTP requests the signature-auth, server-key-proof or device middleware refused",
    ),
    promql_panel(
        pid="syrinx_ws_handshake_rejected",
        title="WebSocket handshake rejections by reason",
        queries=promql_sum_by_values(WS_HANDSHAKE_REJECTED, "reason", WS_HANDSHAKE_REASONS),
        layout=layout(96, 0, 96, 14, 80),
        description="Refused WebSocket handshakes; internal = a server-side error, not the caller",
    ),
    top_table(
        pid="syrinx_auth_rejected_by_user",
        title="Auth rejections by user and key",
        stream=AUTH_REJECTED,
        labels=["user_id", "key_id", "reason", "route"],
        keys=[("key_id", "Key (as claimed)"), ("user_id", "User"), ("reason", "Reason")],
        layout=layout(0, 14, 96, 16, 81),
        description=(
            "Keys behind the most refused requests. The key is as the caller "
            "claimed it; user is empty when the server doesn't know the key."
        ),
    ),
    top_table(
        pid="syrinx_auth_rejected_by_route",
        title="Auth rejections by route",
        stream=AUTH_REJECTED,
        labels=["route", "reason", "key_id", "user_id"],
        keys=[("route", "Route"), ("reason", "Reason")],
        layout=layout(96, 14, 96, 16, 82),
        description="Which endpoints are being probed (mux route template)",
    ),
    promql_panel(
        pid="syrinx_signup_rejected",
        title="Signup rejections by reason",
        queries=promql_sum_by_values(SIGNUP_REJECTED, "reason", SIGNUP_REASONS),
        layout=layout(0, 30, 96, 14, 83),
        description="Refused signups; reserved = a name root reserved",
    ),
    promql_panel(
        pid="syrinx_username_checks",
        title="Username availability checks",
        queries=[
            promql_labeled(USERNAME_CHECKS, "available", "true", "available"),
            promql_labeled(USERNAME_CHECKS, "available", "false", "taken"),
            (f"sum({RESERVED_HITS})", "reserved name hits", RESERVED_HITS),
        ],
        layout=layout(96, 30, 96, 14, 84),
        description="A spike in checks without signups suggests username enumeration",
    ),
    promql_panel(
        pid="syrinx_cooldown_hits",
        title="Cooldown hits",
        queries=promql_sum_by_values(COOLDOWN_HITS, "kind", ["key_rotation", "vouch"]),
        layout=layout(0, 44, 96, 14, 85),
        description="Key rotations and vouches refused for coming too soon",
    ),
    promql_panel(
        pid="syrinx_block_refusals",
        title="Block refusals",
        queries=promql_sum_by_values(BLOCK_REFUSALS, "surface", ["http", "ws"]),
        layout=layout(96, 44, 96, 14, 86),
        description="Reads and requests refused because the author blocked the viewer",
    ),
    top_table(
        pid="syrinx_block_refusals_by_viewer",
        title="Blocked viewers who keep trying",
        stream=BLOCK_REFUSALS,
        labels=["viewer_id", "author_id", "surface"],
        keys=[("viewer_id", "Viewer"), ("author_id", "Blocked by")],
        layout=layout(0, 58, 96, 16, 87),
        description="Viewers repeatedly refused by an author who blocked them",
    ),
    promql_panel(
        pid="syrinx_ws_connections",
        title="Open WebSocket connections",
        queries=[(f"sum({WS_CONNECTIONS})", "connections", WS_CONNECTIONS)],
        layout=layout(96, 58, 96, 16, 88),
        description="Sockets open right now across replicas; resets when a process restarts",
    ),
    top_table(
        pid="syrinx_rejected_by_author",
        title="Top 10 authors of rejected reeds",
        stream=CONTENT_REJECTED,
        labels=["reporter_id", "author_id", "store_name", "reason"],
        keys=[("author_id", "Author")],
        layout=layout(0, 74, 96, 16, 100),
        limit=10,
        description="Authors whose reeds or threads clients refused most",
    ),
    top_table(
        pid="syrinx_rejected_by_reporter",
        title="Top 10 users rejecting content",
        stream=CONTENT_REJECTED,
        labels=["reporter_id", "author_id", "store_name", "reason"],
        keys=[("reporter_id", "User")],
        layout=layout(96, 74, 96, 16, 101),
        limit=10,
        description="Users whose clients refused the most content, of any kind",
    ),
]

dashboard = {
    "version": 8,
    "title": "Syrinx",
    "description": (
        "Application golden signals from HTTP/DB traces, otelsql pool metrics, "
        "and syrinx.* business counters. Host CPU/memory/disk live on the "
        "separate Host Metrics dashboard."
    ),
    "role": "",
    "owner": "",
    "created": "2026-08-04T00:00:00Z",
    "tabs": [
        {"tabId": "overview", "name": "Overview", "panels": overview},
        {"tabId": "requests", "name": "Requests", "panels": requests},
        {"tabId": "database", "name": "Database", "panels": database},
        {"tabId": "crypto", "name": "Crypto", "panels": crypto},
        {"tabId": "federation", "name": "Federation", "panels": federation},
        {"tabId": "users", "name": "Users", "panels": users},
        {"tabId": "reeds", "name": "Reeds", "panels": reeds},
        {"tabId": "websocket", "name": "WebSocket", "panels": websocket},
        {"tabId": "relay", "name": "Relay", "panels": relay},
        {"tabId": "admin", "name": "Admin", "panels": admin},
        {"tabId": "abuse", "name": "Abuse", "panels": abuse},
    ],
    "variables": {"list": [], "showDynamicFilters": False},
    "defaultDatetimeDuration": {
        "type": "relative",
        "relativeTimePeriod": "15m",
    },
}


def main() -> None:
    OUT.write_text(json.dumps(dashboard, indent=2) + "\n")
    print(f"Wrote {OUT} ({OUT.stat().st_size} bytes)")


if __name__ == "__main__":
    main()
