#!/usr/bin/env python3
"""Find a dashboard's id + hash by title from a dashboards-list API response.

Reads the JSON body of GET /api/{org}/dashboards on stdin. Prints
"<dashboard_id> <hash>" and exits 0 if found; prints nothing and exits 0
if not found or the response can't be parsed (caller treats "not found"
as "create it").

Usage: find_dashboard_by_title.py <title>
"""
import json
import sys


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: find_dashboard_by_title.py <title>", file=sys.stderr)
        return 2
    title = sys.argv[1]

    try:
        data = json.load(sys.stdin)
    except Exception:
        return 0

    for d in data.get("dashboards") or []:
        if d.get("title") == title:
            print(d.get("dashboard_id", ""), d.get("hash", ""))
            break
    return 0


if __name__ == "__main__":
    sys.exit(main())
