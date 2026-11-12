#!/usr/bin/env python3
"""Generate one fully-populated, correctly-typed GET-200 response per endpoint the
exporter decodes, straight from the Veritas OpenAPI specs under docs/veritas-*.

The samples (testdata/spec-samples/*.json) are decoded by the Go models in
internal/models/spec_samples_test.go: a model field whose JSON type disagrees with
the spec (e.g. an int where the spec returns a string cursor) fails that test.

Run from the repo root:  python3 scripts/gen_spec_samples.py   (needs PyYAML)
"""
import json
import os
import sys

import yaml

# endpoint -> (spec guide, sample file name). The newest spec defining the
# endpoint wins (11.2 = API 14.0, then 11.0 = 13.0, then 10.5 = 12.0).
ENDPOINTS = {
    "/admin/jobs": ("admin.yaml", "jobs"),
    "/storage/storage-units": ("storage.yaml", "storage-units"),
    "/storage/drives": ("storage.yaml", "drives"),
    "/storage/tape-media": ("storage.yaml", "tape-media"),
    "/storage/tape-volume-pools": ("storage.yaml", "tape-volume-pools"),
    "/storage/disk-pools": ("storage.yaml", "disk-pools"),
    "/storage/robots-device-hosts": ("storage.yaml", "robots-device-hosts"),
    "/manage/alerts": ("manage.yaml", "alerts"),
    "/malware/latest-scan-results": ("malware.yaml", "malware-scans"),
    "/catalog/images": ("catalog.yaml", "catalog-images"),
    "/servicecatalog/slos": ("servicecatalog.yaml", "slos"),
}
SPECS = ["11.2", "11.0", "10.5"]


def resolve(spec, node):
    while isinstance(node, dict) and "$ref" in node:
        cur = spec
        for part in node["$ref"].lstrip("#/").split("/"):
            cur = cur[part]
        node = cur
    return node


def sample(spec, schema, depth=0):
    schema = resolve(spec, schema)
    if not isinstance(schema, dict) or depth > 12:
        return None
    for key in ("allOf",):
        if key in schema:
            out = {}
            for sub in schema[key]:
                val = sample(spec, sub, depth + 1)
                if isinstance(val, dict):
                    out.update(val)
            for name, sub in (schema.get("properties") or {}).items():
                out[name] = sample(spec, sub, depth + 1)
            return out
    for key in ("oneOf", "anyOf"):
        if key in schema:
            branches = [sample(spec, b, depth + 1) for b in schema[key]]
            if all(isinstance(b, dict) for b in branches):
                # Object alternatives (e.g. tape vs disk storage-unit fields): merge
                # every branch plus sibling properties so each alternative field is
                # represented and checked against the models.
                merged = {name: sample(spec, sub, depth + 1)
                          for name, sub in (schema.get("properties") or {}).items()}
                for b in branches:
                    merged.update(b)
                return merged
            return branches[0]
    if "enum" in schema and schema["enum"]:
        return schema["enum"][0]
    typ = schema.get("type")
    if typ == "object" or "properties" in schema:
        return {name: sample(spec, sub, depth + 1)
                for name, sub in (schema.get("properties") or {}).items()}
    if typ == "array":
        item = sample(spec, schema.get("items", {}), depth + 1)
        return [item] if item is not None else []
    if typ == "integer":
        return 1
    if typ == "number":
        return 1.5
    if typ == "boolean":
        return True
    if typ == "string":
        fmt = schema.get("format")
        if fmt == "date-time":
            return "2026-11-12T10:00:00.000Z"
        if fmt in ("uuid",):
            return "00000000-0000-0000-0000-000000000001"
        return "sample"
    return None


def main():
    repo = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    out_dir = os.path.join(repo, "testdata", "spec-samples")
    os.makedirs(out_dir, exist_ok=True)
    index = {}
    for endpoint, (guide, name) in ENDPOINTS.items():
        for ver in SPECS:
            path = os.path.join(repo, "docs", f"veritas-{ver}", guide)
            if not os.path.exists(path):
                continue
            spec = yaml.safe_load(open(path))
            if endpoint not in spec.get("paths", {}):
                continue
            content = spec["paths"][endpoint]["get"]["responses"]["200"]["content"]
            media = next(iter(content))
            body = sample(spec, content[media]["schema"])
            with open(os.path.join(out_dir, f"{name}.json"), "w") as f:
                json.dump(body, f, indent=1, sort_keys=True)
                f.write("\n")
            index[name] = {"endpoint": endpoint, "spec": f"veritas-{ver}/{guide}", "mediaType": media}
            break
        else:
            print(f"no spec defines {endpoint}", file=sys.stderr)
            return 1
    with open(os.path.join(out_dir, "INDEX.json"), "w") as f:
        json.dump(index, f, indent=1, sort_keys=True)
        f.write("\n")
    for name, meta in sorted(index.items()):
        print(f"{name:22} {meta['spec']:32} {meta['mediaType']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
