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


def promql_labeled(
    metric: str, attr: str, value: str, legend: str
) -> tuple[str, str, str]:
    return (f'{metric}{{{attr}="{value}"}}', legend, metric)


def promql_filter(
    metric: str, labels: dict[str, str], legend: str
) -> tuple[str, str, str]:
    parts = ",".join(f'{k}="{v}"' for k, v in labels.items())
    return (f"{metric}{{{parts}}}", legend, metric)


def histogram_avg(metric: str, legend: str) -> tuple[str, str, str]:
    """Mean observation value. OTLP histograms land as separate _sum/_count streams in O2."""
    return (f"{metric}_sum / {metric}_count", legend, f"{metric}_sum")


def histogram_samples(metric: str, legend: str) -> tuple[str, str, str]:
    """Number of histogram recordings in the window (useful for sparse series)."""
    return (f"{metric}_count", legend, f"{metric}_count")


def histogram_quantile_query(
    metric: str, quantile: float, legend: str
) -> tuple[str, str, str]:
    """Percentile band from OTLP histogram bucket series (real distribution, not mean).

    No rate(): each reed/observation gets its own series (reed_id label), recorded
    once ever, so rate() over a single sample is undefined and returns NaN. These
    are cumulative-per-observation buckets, so sum by (le) directly is correct —
    verified against the live syrinx OpenObserve instance (p50/p90/p99 all returned
    sane increasing values without rate(); with rate() every quantile was NaN).
    """
    return (
        f"histogram_quantile({quantile}, sum({metric}_bucket) by (le))",
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


def promql_labeled_queries(
    metric: str, attr: str, pairs: list[tuple[str, str]]
) -> list[tuple[str, str, str]]:
    return [promql_labeled(metric, attr, value, legend) for value, legend in pairs]


def promql_ws_queries(
    direction: str, pairs: list[tuple[str, str]]
) -> list[tuple[str, str, str]]:
    return [
        promql_filter(
            WS_MESSAGES,
            {"ws_direction": direction, "ws_message_type": msg_type},
            legend,
        )
        for msg_type, legend in pairs
    ]


# Grid is 192 units wide (OpenObserve v8 after Host Metrics migration scale).
# Half = 96, quarter = 48, third ≈ 64.


def layout(x: int, y: int, w: int, h: int, i: int) -> dict:
    return {"x": x, "y": y, "w": w, "h": h, "i": i}


def metric_stream(instrument: str) -> str:
    """OTEL name (syrinx.users.created) → OpenObserve metrics stream."""
    return instrument.replace(".", "_")


# syrinx.* business metrics (spec observability/05_custom_metrics.md).
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
ECHOES_TARGETED = metric_stream("syrinx.echoes.targeted")
REEDS_REJECTED_LENGTH = metric_stream("syrinx.reeds.rejected.length")
REED_RAW_CHARS = metric_stream("syrinx.reed.content.raw_chars")
REED_VISIBLE_CHARS = metric_stream("syrinx.reed.content.visible_chars")
REED_HOLDERS = metric_stream("syrinx.reed.holders")
REED_COVERAGE = metric_stream("syrinx.reed.coverage_percent")
WS_MESSAGES = metric_stream("syrinx.ws.messages")

# High-signal WS types from spec 5.4 (full enum lives in Explore).
WS_IN_TYPES = [
    ("PING", "PING"),
    ("REQUEST_REED", "REQUEST_REED"),
    ("DATA_ACK", "DATA_ACK"),
    ("SYNC_REQUEST", "SYNC_REQUEST"),
    ("RELAY_RESPONSE", "RELAY_RESPONSE"),
    ("RELAY_MISS", "RELAY_MISS"),
    ("SUBSCRIBE_REED", "SUBSCRIBE_REED"),
]
WS_OUT_TYPES = [
    ("RELAY_REQUEST", "RELAY_REQUEST"),
    ("DATA_RESPONSE", "DATA_RESPONSE"),
    ("BROADCAST_REED", "BROADCAST_REED"),
    ("REED_COVERAGE", "REED_COVERAGE"),
    ("REQUEST_ACK", "REQUEST_ACK"),
    ("pong", "pong"),
    ("REED_ECHOES", "REED_ECHOES"),
    ("REED_REPLIES", "REED_REPLIES"),
]


HTTP = "span_kind = '2'"
DB = "db_system = 'postgresql'"
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
            (USERS_CREATED, "total", USERS_CREATED),
            (f'{USERS_CREATED}{{signup_mode="open"}}', "open", USERS_CREATED),
            (f'{USERS_CREATED}{{signup_mode="invite"}}', "invite", USERS_CREATED),
            (f'{USERS_CREATED}{{signup_mode="closed"}}', "closed", USERS_CREATED),
        ],
        layout=layout(0, 0, 96, 14, 30),
        description="Successful signups by signup.mode (open / invite / closed)",
    ),
    promql_panel(
        pid="syrinx_users_deleted",
        title="Account deletions",
        queries=[
            (USERS_DELETED, "total", USERS_DELETED),
            (f'{USERS_DELETED}{{note_has="true"}}', "with note", USERS_DELETED),
            (f'{USERS_DELETED}{{note_has="false"}}', "silent", USERS_DELETED),
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
        queries=[(KEYS_REVOKED, "revoked", KEYS_REVOKED)],
        layout=layout(0, 14, 96, 14, 32),
        description="Successful key revocations; per-user series carry user.id in Explore",
    ),
    promql_panel(
        pid="syrinx_user_churn",
        title="Signups vs deletions",
        queries=[
            (USERS_CREATED, "created", USERS_CREATED),
            (USERS_DELETED, "deleted", USERS_DELETED),
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
            (USERS_BACKUP, "total", USERS_BACKUP),
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
    sql_panel(
        pid="syrinx_user_retention",
        title="Signup cohort retention (% still active by day N)",
        ptype="line",
        sql=(
            "WITH signups AS ( "
            "SELECT user_id_hash, min(_timestamp) AS signed_up_at "
            f"FROM {USERS_CREATED} WHERE user_id_hash IS NOT NULL "
            "GROUP BY user_id_hash ), "
            "deletions AS ( "
            "SELECT user_id_hash, min(_timestamp) AS deleted_at "
            f"FROM {USERS_DELETED} WHERE user_id_hash IS NOT NULL "
            "GROUP BY user_id_hash ) "
            "SELECT floor((extract(epoch FROM now()) * 1000000.0 - s.signed_up_at) "
            "/ 86400000000.0) AS days_active, "
            "count(*) AS cohort_still_active "
            "FROM signups s LEFT JOIN deletions d ON s.user_id_hash = d.user_id_hash "
            "WHERE d.deleted_at IS NULL "
            "GROUP BY days_active ORDER BY days_active"
        ),
        layout=layout(0, 56, 192, 14, 38),
        x=[axis("Days since signup", "days_active", sort_by="ASC")],
        y=[axis("Signups still active", "cohort_still_active")],
        stream=USERS_CREATED,
        stream_type="metrics",
        description=(
            "Per-user (user_id_hash) signup timestamp, excluded once a matching "
            "deletion timestamp exists (both from raw histogram exemplars). "
            "user_id_hash IS NOT NULL filters out pre-hash datapoints from before "
            "this label was added. Not cohort-bucketed by signup week — just "
            "'signups from N days ago still active today', across all hashed "
            "signups to date. Reads as ~100% flat until syrinx_users_deleted has "
            "data (no deletions recorded as of authoring). extract(epoch FROM now()) "
            "and floor() verified directly against this OpenObserve instance's "
            "DataFusion SQL engine; datediff/to_unix_timestamp are NOT supported here."
        ),
    ),
]

reeds = [
    promql_panel(
        pid="syrinx_reeds_published",
        title="Publishes by kind",
        queries=[
            (REEDS_PUBLISHED, "total", REEDS_PUBLISHED),
            *promql_labeled_queries(
                REEDS_PUBLISHED,
                "reed_kind",
                [("plain", "plain"), ("echo", "echo"), ("reply", "reply")],
            ),
        ],
        layout=layout(0, 0, 96, 14, 40),
        description="Successful SignReed creates (not replays), split by reed.kind",
    ),
    promql_panel(
        pid="syrinx_echoes_targeted",
        title="Echoes sent vs received",
        queries=[
            promql_labeled(REEDS_PUBLISHED, "reed_kind", "echo", "echo sent"),
            (ECHOES_TARGETED, "echo indexed on target", ECHOES_TARGETED),
        ],
        layout=layout(96, 0, 96, 14, 41),
        description=(
            "Echo sent = new echo reed published; targeted = original reed indexed "
            "(syrinx.echoes.targeted)"
        ),
    ),
    promql_panel(
        pid="syrinx_reeds_deleted",
        title="Reed deletions",
        queries=[(REEDS_DELETED, "deleted", REEDS_DELETED)],
        layout=layout(0, 14, 48, 14, 42),
        description=(
            "Successful reed removal certs (not replays). "
            "No Data = no deletions in the selected window."
        ),
    ),
    promql_panel(
        pid="syrinx_reeds_rejected_length",
        title="Length rejections",
        queries=[
            (REEDS_REJECTED_LENGTH, "total", REEDS_REJECTED_LENGTH),
            promql_labeled(
                REEDS_REJECTED_LENGTH, "raw_exceeds_max", "true", "raw over max"
            ),
            promql_labeled(
                REEDS_REJECTED_LENGTH,
                "visible_exceeds_max",
                "true",
                "visible over max",
            ),
        ],
        layout=layout(48, 14, 48, 14, 43),
        description=(
            "SignReed HTTP 400 from ReedContentWithinLimits (max raw 1400, visible 140). "
            "No Data = no rejections in the selected window."
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
        pid="syrinx_reed_raw_chars",
        title="Reed body length (raw chars) — p50/p90/p99",
        ptype="area",
        queries=histogram_percentile_bands(REED_RAW_CHARS),
        layout=layout(0, 28, 96, 14, 45),
        description=(
            "Shape of len(body) on successful publish, from OTLP histogram buckets "
            "(not just the mean). Cap is 1400 raw chars — p99 hugging the cap may "
            "indicate abuse. Bucket boundaries are SDK defaults, coarse between "
            "100–1400; see syrinx/observability for tuning a View if more "
            "resolution near the cap is needed."
        ),
    ),
    promql_panel(
        pid="syrinx_reed_visible_chars",
        title="Reed body length (visible chars) — p50/p90/p99",
        ptype="area",
        queries=histogram_percentile_bands(REED_VISIBLE_CHARS),
        layout=layout(96, 28, 96, 14, 46),
        description=(
            "Shape of CountMarkdownCharacters(body) on publish. Cap is 140 visible chars."
        ),
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
        layout=layout(0, 42, 192, 14, 47),
        description=(
            "holders and coverage % are the same event (ReedCoverage records both "
            "together — coverage % = holders / active users) so they're merged here. "
            "Shape from OTLP histogram buckets instead of a flattened mean. "
            "Per-reed series (author.id + reed.id) aggregate; filter in Explore."
        ),
    ),
]

websocket = [
    promql_panel(
        pid="syrinx_ws_volume",
        title="WebSocket messages",
        queries=[
            (WS_MESSAGES, "total", WS_MESSAGES),
            promql_labeled(WS_MESSAGES, "ws_direction", "in", "in"),
            promql_labeled(WS_MESSAGES, "ws_direction", "out", "out"),
        ],
        layout=layout(0, 0, 96, 14, 50),
        description="Every handled WS frame (spec 5.4 normalized message types)",
    ),
    promql_panel(
        pid="syrinx_ws_in_types",
        title="Inbound by message type",
        queries=promql_ws_queries("in", WS_IN_TYPES),
        layout=layout(96, 0, 96, 14, 51),
        description="Client → server frames (ws.direction=in)",
    ),
    promql_panel(
        pid="syrinx_ws_out_types",
        title="Outbound by message type",
        queries=promql_ws_queries("out", WS_OUT_TYPES),
        layout=layout(0, 14, 192, 14, 52),
        description="Server → client frames (ws.direction=out)",
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
        layout=layout(0, 28, 192, 14, 53),
        description="Quick view of reed relay request/response/miss balance",
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
        {"tabId": "users", "name": "Users", "panels": users},
        {"tabId": "reeds", "name": "Reeds", "panels": reeds},
        {"tabId": "websocket", "name": "WebSocket", "panels": websocket},
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
