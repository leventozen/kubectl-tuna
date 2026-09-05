#!/usr/bin/env python3
"""Create a durable, identity-minimized summary from a successful E2E run."""

import argparse
import json
import re
import sys
from pathlib import Path


def fail(message: str) -> None:
    raise ValueError(message)


def load_object(path: Path) -> dict:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        fail(f"cannot read {path}: {error}")
    if not isinstance(value, dict):
        fail(f"{path} must contain a JSON object")
    return value


def require_string(value: object, field: str) -> str:
    if not isinstance(value, str) or not value:
        fail(f"{field} must be a non-empty string")
    return value


def summarize(artifact_dir: Path) -> dict:
    environment = load_object(artifact_dir / "environment.json")
    if environment.get("schemaVersion") != 2:
        fail("environment schemaVersion must be 2")
    if environment.get("evidenceClass") != "live-reproduction":
        fail("environment evidenceClass must be live-reproduction")

    tuna = environment.get("tuna")
    if not isinstance(tuna, dict):
        fail("environment.tuna must be an object")
    source_commit = require_string(tuna.get("sourceCommit"), "tuna.sourceCommit")
    if not re.fullmatch(r"[0-9a-f]{40}", source_commit):
        fail("tuna.sourceCommit must be a full lowercase Git commit")
    if tuna.get("sourceDirty") is not False:
        fail("durable baselines require a clean Tuna source tree")

    execution = environment.get("execution")
    if not isinstance(execution, dict):
        fail("environment.execution must be an object")
    expected_version = require_string(
        execution.get("expectedKubernetes"), "execution.expectedKubernetes"
    )
    node_image = require_string(execution.get("kindNodeImage"), "execution.kindNodeImage")
    if not re.fullmatch(r"kindest/node:v[^@]+@sha256:[0-9a-f]{64}", node_image):
        fail("execution.kindNodeImage must be a digest-pinned Kind image")

    api_server = environment.get("apiServer")
    if not isinstance(api_server, dict):
        fail("environment.apiServer must be an object")
    server_version = require_string(api_server.get("gitVersion"), "apiServer.gitVersion")
    if server_version != f"v{expected_version}":
        fail(
            f"API server {server_version} does not match expected Kubernetes "
            f"v{expected_version}"
        )

    raw_nodes = environment.get("nodes")
    if not isinstance(raw_nodes, list) or not raw_nodes:
        fail("environment.nodes must contain at least one Node")
    nodes = []
    node_fields = (
        "kubeletVersion",
        "containerRuntimeVersion",
        "operatingSystem",
        "osImage",
        "architecture",
        "kernelVersion",
    )
    for index, node in enumerate(raw_nodes):
        if not isinstance(node, dict):
            fail(f"environment.nodes[{index}] must be an object")
        nodes.append(
            {
                field: require_string(node.get(field), f"nodes[{index}].{field}")
                for field in node_fields
            }
        )
    nodes.sort(key=lambda item: tuple(item[field] for field in node_fields))

    case_dir = artifact_dir / "cases"
    case_paths = sorted(case_dir.glob("*.json"))
    if not case_paths:
        fail("artifact contains no completed E2E case results")

    cases = []
    for path in case_paths:
        result = load_object(path)
        health = result.get("health")
        if health not in {"healthy", "degraded", "unknown"}:
            fail(f"{path}: unsupported health {health!r}")
        if not isinstance(result.get("partial"), bool):
            fail(f"{path}: partial must be a boolean")

        cluster = result.get("cluster")
        if not isinstance(cluster, dict) or cluster.get("kubernetesVersion") != server_version:
            fail(f"{path}: result Kubernetes version does not match environment")
        focus = result.get("focus")
        if not isinstance(focus, dict):
            fail(f"{path}: focus must be an object")

        findings = result.get("findings")
        root_ids = result.get("rootCauses")
        warnings = result.get("warnings", [])
        rules = result.get("rules")
        if not isinstance(findings, list) or not isinstance(root_ids, list):
            fail(f"{path}: findings and rootCauses must be arrays")
        if not isinstance(warnings, list) or not isinstance(rules, dict):
            fail(f"{path}: warnings and rules have invalid shapes")

        finding_by_id = {}
        flagged_roots = []
        finding_types = []
        for finding in findings:
            if not isinstance(finding, dict):
                fail(f"{path}: each finding must be an object")
            finding_id = require_string(finding.get("id"), f"{path}: finding.id")
            if finding_id in finding_by_id:
                fail(f"{path}: duplicate finding ID {finding_id}")
            finding_by_id[finding_id] = finding
            finding_types.append(require_string(finding.get("type"), f"{path}: finding.type"))
            if finding.get("rootCause") is True:
                flagged_roots.append(finding_id)
        if sorted(root_ids) != sorted(flagged_roots):
            fail(f"{path}: rootCauses disagree with rootCause flags")

        for root_id in root_ids:
            if not isinstance(root_id, str) or root_id not in finding_by_id:
                fail(f"{path}: rootCauses contains an unknown finding ID")
        root_types = sorted(finding_by_id[root_id]["type"] for root_id in root_ids)
        warning_sources = []
        for warning in warnings:
            if not isinstance(warning, dict):
                fail(f"{path}: each warning must be an object")
            warning_sources.append(
                require_string(warning.get("source"), f"{path}: warning.source")
            )

        skipped = rules.get("skipped", [])
        if not isinstance(skipped, list):
            fail(f"{path}: rules.skipped must be an array")
        evaluated = rules.get("evaluated")
        if isinstance(evaluated, bool) or not isinstance(evaluated, int) or evaluated < 0:
            fail(f"{path}: rules.evaluated must be a non-negative integer")
        skipped_ids = sorted(
            require_string(item.get("id"), f"{path}: rules.skipped.id")
            for item in skipped
            if isinstance(item, dict)
        )
        if len(skipped_ids) != len(skipped):
            fail(f"{path}: each skipped rule must be an object")

        cases.append(
            {
                "id": path.stem,
                "focusKind": require_string(focus.get("kind"), f"{path}: focus.kind"),
                "health": health,
                "partial": result["partial"],
                "findingTypes": sorted(finding_types),
                "rootCauseTypes": root_types,
                "warningSources": sorted(warning_sources),
                "rules": {
                    "evaluated": evaluated,
                    "skippedIDs": skipped_ids,
                    "suspended": bool(rules.get("suspendedReason")),
                },
            }
        )

    return {
        "schemaVersion": 1,
        "evidenceClass": "live-reproduction",
        "capturedAt": require_string(environment.get("capturedAt"), "capturedAt"),
        "tuna": {
            "sourceCommit": source_commit,
            "goVersion": require_string(tuna.get("goVersion"), "tuna.goVersion"),
        },
        "kubernetes": {
            "distribution": require_string(environment.get("distribution"), "distribution"),
            "expectedVersion": expected_version,
            "kindNodeImage": node_image,
            "apiServer": api_server,
            "nodes": nodes,
        },
        "cases": cases,
    }


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("artifact_dir", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()

    try:
        if args.output.exists():
            fail(f"refusing to overwrite existing output: {args.output}")
        baseline = summarize(args.artifact_dir)
    except ValueError as error:
        print(f"summarize-e2e-baseline: {error}", file=sys.stderr)
        raise SystemExit(1) from error
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(baseline, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    print(f"wrote durable E2E baseline summary to {args.output}")


if __name__ == "__main__":
    main()
