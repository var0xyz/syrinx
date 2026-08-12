#!/usr/bin/env python3
"""Warn if a live OpenObserve dashboard has drifted from its bundled JSON.

Reads the JSON body of GET /api/{org}/dashboards/{id} on stdin and compares
it against the local dashboard file, ignoring server-only metadata
(dashboard_id, hash, owner, created) and int/float differences that can
appear from a round trip through the API's numeric (de)serialization.

Prints a warning to stdout and exits 1 if the two differ; exits 0 (silently)
if they match, or if the comparison can't be made confidently (parse
failure, unexpected response shape) — callers treat this as "never block
the run, only advise".

Usage: check_dashboard_drift.py <local-dashboard-file> <name>
"""
import json
import sys

IGNORED_KEYS = {"dashboard_id", "hash", "owner", "created"}


def normalize(v):
    if isinstance(v, dict):
        return {k: normalize(x) for k, x in v.items() if k not in IGNORED_KEYS}
    if isinstance(v, list):
        return [normalize(x) for x in v]
    if isinstance(v, bool):
        return v
    if isinstance(v, (int, float)):
        return float(v)
    return v


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: check_dashboard_drift.py <local-dashboard-file> <name>", file=sys.stderr)
        return 2
    local_file, name = sys.argv[1], sys.argv[2]

    try:
        with open(local_file) as f:
            local = normalize(json.load(f))
        remote = normalize(json.loads(sys.stdin.read()))
    except Exception:
        return 0

    # Bail out quietly on an unexpected response shape (API version mismatch,
    # error body, etc.) instead of risking a false-positive drift warning.
    if not isinstance(remote, dict) or "tabs" not in remote:
        return 0

    if local != remote:
        print(f"⚠️  Live \"{name}\" dashboard has drifted from the bundled JSON")
        print("   (edited in the OpenObserve UI since the last update?)")
        print(f"   update.sh is about to overwrite it with {local_file}.")
        print("   Save any manual changes from the UI first if you want to keep them.")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
