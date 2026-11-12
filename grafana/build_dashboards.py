#!/usr/bin/env python3
"""Generate all nbu_exporter Grafana dashboards from grafana/gen/.

Run from the repo root:  python3 grafana/build_dashboards.py
Each dashboard is validated against the known nbu_* metric set and the multi-site
template-variable contract before writing.
"""
import json
import sys

from grafana.gen import overview, jobs, storage, dataprotection, tape, lifecycle, multisite
from grafana.gen.validate import assign_ref_ids, check_dashboard, check_ref_ids, check_site_wiring

OUTPUTS = [
    ("grafana/nbu-overview.json", overview.build),
    ("grafana/nbu-jobs.json", jobs.build),
    ("grafana/nbu-storage.json", storage.build),
    ("grafana/nbu-dataprotection.json", dataprotection.build),
    ("grafana/nbu-tape.json", tape.build),
    ("grafana/nbu-lifecycle.json", lifecycle.build),
    ("grafana/nbu-multisite.json", multisite.build),
]


def main():
    failures = []
    for path, build in OUTPUTS:
        dash = assign_ref_ids(build())
        unknown = check_dashboard(dash)
        if unknown:
            failures.append(f"{path}: unknown metrics {unknown}")
            continue
        dup = check_ref_ids(dash)
        if dup:
            failures.append(f"{path}: duplicate refIds in {dup}")
            continue
        site_problems = check_site_wiring(dash)
        if site_problems:
            failures.append(f"{path}: multi-site wiring: {site_problems}")
            continue
        with open(path, "w") as f:
            json.dump(dash, f, indent=2)
            f.write("\n")
        print(f"wrote {path} ({len(dash['panels'])} panels)")
    if failures:
        for msg in failures:
            print(f"ERROR: {msg}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
