#!/usr/bin/env python3
"""Validate committed E2E compatibility baseline sets and their provenance."""

import argparse
import json
import re
import sys
from pathlib import Path, PurePosixPath


COMMIT_RE = re.compile(r"[0-9a-f]{40}")
DIGEST_RE = re.compile(r"sha256:[0-9a-f]{64}")
REPOSITORY_RE = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+")
SET_ID_RE = re.compile(r"run-[1-9][0-9]*")
VERSION_RE = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+")
FORBIDDEN_IDENTITY_KEYS = {"clusterName", "name", "namespace", "uid"}
OUTCOME_KEYS = (
    "focusKind",
    "health",
    "partial",
    "rootCauseTypes",
    "warningSources",
    "rules",
)


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


def require_sorted_strings(value: object, field: str) -> list[str]:
    if not isinstance(value, list) or any(
        not isinstance(item, str) or not item for item in value
    ):
        fail(f"{field} must be an array of non-empty strings")
    if value != sorted(value):
        fail(f"{field} must be sorted")
    return value


def reject_identity_keys(value: object, location: str) -> None:
    if isinstance(value, dict):
        for key, child in value.items():
            if key in FORBIDDEN_IDENTITY_KEYS:
                fail(f"{location} contains forbidden identity key {key!r}")
            reject_identity_keys(child, f"{location}.{key}")
    elif isinstance(value, list):
        for index, child in enumerate(value):
            reject_identity_keys(child, f"{location}[{index}]")


def validate_baseline(
    path: Path, entry: dict, source_commit: str
) -> tuple[list[str], dict]:
    baseline = load_object(path)
    if baseline.get("schemaVersion") != 1:
        fail(f"{path}: schemaVersion must be 1")
    if baseline.get("evidenceClass") != "live-reproduction":
        fail(f"{path}: evidenceClass must be live-reproduction")
    reject_identity_keys(baseline, str(path))
    require_string(baseline.get("capturedAt"), f"{path}: capturedAt")

    tuna = baseline.get("tuna")
    if not isinstance(tuna, dict) or tuna.get("sourceCommit") != source_commit:
        fail(f"{path}: Tuna source commit does not match its baseline set")
    require_string(tuna.get("goVersion"), f"{path}: tuna.goVersion")

    suite = baseline.get("suite")
    if not isinstance(suite, dict) or suite.get("full") is not True:
        fail(f"{path}: suite must be a successful full run")
    selected_cases = suite.get("selectedCases")
    if not isinstance(selected_cases, list) or not selected_cases or any(
        not isinstance(case, str) or not case for case in selected_cases
    ):
        fail(f"{path}: suite.selectedCases must be a non-empty string array")
    if len(selected_cases) != len(set(selected_cases)):
        fail(f"{path}: suite.selectedCases contains duplicates")

    version = require_string(entry.get("kubernetesVersion"), "kubernetesVersion")
    if not VERSION_RE.fullmatch(version):
        fail(f"{path}: Kubernetes version must use major.minor.patch")
    kubernetes = baseline.get("kubernetes")
    if not isinstance(kubernetes, dict):
        fail(f"{path}: kubernetes must be an object")
    require_string(kubernetes.get("distribution"), f"{path}: distribution")
    if kubernetes.get("expectedVersion") != version:
        fail(f"{path}: expected Kubernetes version does not match the index")
    image = require_string(kubernetes.get("kindNodeImage"), f"{path}: kindNodeImage")
    if not re.fullmatch(
        rf"kindest/node:v{re.escape(version)}@sha256:[0-9a-f]{{64}}", image
    ):
        fail(f"{path}: Kind image tag does not match Kubernetes {version}")
    api_server = kubernetes.get("apiServer")
    if not isinstance(api_server, dict) or api_server.get("gitVersion") != f"v{version}":
        fail(f"{path}: API server version does not match Kubernetes {version}")
    nodes = kubernetes.get("nodes")
    if not isinstance(nodes, list) or not nodes:
        fail(f"{path}: kubernetes.nodes must be a non-empty array")

    cases = baseline.get("cases")
    if not isinstance(cases, list) or not cases:
        fail(f"{path}: cases must be a non-empty array")
    case_ids = []
    outcomes = {}
    for index, case in enumerate(cases):
        if not isinstance(case, dict):
            fail(f"{path}: cases[{index}] must be an object")
        case_id = require_string(case.get("id"), f"{path}: cases[{index}].id")
        case_ids.append(case_id)
        require_string(case.get("focusKind"), f"{path}: case {case_id} focusKind")
        if case.get("health") not in {"healthy", "degraded", "unknown"}:
            fail(f"{path}: case {case_id} has unsupported health")
        if not isinstance(case.get("partial"), bool):
            fail(f"{path}: case {case_id} partial must be a boolean")
        require_sorted_strings(
            case.get("findingTypes"), f"{path}: case {case_id} findingTypes"
        )
        require_sorted_strings(
            case.get("rootCauseTypes"), f"{path}: case {case_id} rootCauseTypes"
        )
        require_sorted_strings(
            case.get("warningSources"), f"{path}: case {case_id} warningSources"
        )
        rules = case.get("rules")
        if not isinstance(rules, dict):
            fail(f"{path}: case {case_id} rules must be an object")
        evaluated = rules.get("evaluated")
        if (
            isinstance(evaluated, bool)
            or not isinstance(evaluated, int)
            or evaluated < 0
        ):
            fail(f"{path}: case {case_id} rules.evaluated must be non-negative")
        if not isinstance(rules.get("suspended"), bool):
            fail(f"{path}: case {case_id} rules.suspended must be a boolean")
        require_sorted_strings(
            rules.get("skippedIDs"), f"{path}: case {case_id} skippedIDs"
        )
        outcomes[case_id] = {key: case.get(key) for key in OUTCOME_KEYS}
    if case_ids != sorted(case_ids) or len(case_ids) != len(set(case_ids)):
        fail(f"{path}: case IDs must be unique and sorted")

    return selected_cases, outcomes


def validate(root: Path) -> dict:
    root = root.resolve()
    index_path = root / "index.json"
    index = load_object(index_path)
    if index.get("schemaVersion") != 1:
        fail("baseline index schemaVersion must be 1")
    sets = index.get("sets")
    if not isinstance(sets, list) or not sets:
        fail("baseline index sets must be a non-empty array")

    indexed_paths = set()
    total_baselines = 0
    set_ids = []
    workflow_runs = []
    for set_index, baseline_set in enumerate(sets):
        if not isinstance(baseline_set, dict):
            fail(f"sets[{set_index}] must be an object")
        set_id = require_string(baseline_set.get("id"), f"sets[{set_index}].id")
        if not SET_ID_RE.fullmatch(set_id):
            fail(f"sets[{set_index}].id must use run-<workflow-id>")
        set_ids.append(set_id)
        repository = require_string(
            baseline_set.get("repository"), f"sets[{set_index}].repository"
        )
        if not REPOSITORY_RE.fullmatch(repository):
            fail(f"sets[{set_index}].repository must use owner/name")
        workflow_run = baseline_set.get("workflowRun")
        if (
            isinstance(workflow_run, bool)
            or not isinstance(workflow_run, int)
            or workflow_run < 1
        ):
            fail(f"sets[{set_index}].workflowRun must be a positive integer")
        if set_id != f"run-{workflow_run}":
            fail(f"sets[{set_index}].id does not match workflowRun")
        workflow_runs.append(workflow_run)
        source_commit = require_string(
            baseline_set.get("sourceCommit"), f"sets[{set_index}].sourceCommit"
        )
        if not COMMIT_RE.fullmatch(source_commit):
            fail(f"sets[{set_index}].sourceCommit must be a full lowercase commit")

        entries = baseline_set.get("baselines")
        if not isinstance(entries, list) or not entries:
            fail(f"sets[{set_index}].baselines must be a non-empty array")
        versions = []
        reference_selection = None
        reference_outcomes = None
        for entry_index, entry in enumerate(entries):
            if not isinstance(entry, dict):
                fail(f"sets[{set_index}].baselines[{entry_index}] must be an object")
            version = require_string(
                entry.get("kubernetesVersion"),
                f"sets[{set_index}].baselines[{entry_index}].kubernetesVersion",
            )
            if not VERSION_RE.fullmatch(version):
                fail(
                    "baseline Kubernetes version must use major.minor.patch: "
                    f"{version}"
                )
            versions.append(tuple(int(part) for part in version.split(".")))
            artifact = require_string(entry.get("artifact"), "artifact")
            if artifact != f"tuna-e2e-kubernetes-{version}":
                fail(f"baseline artifact name does not match Kubernetes {version}")
            digest = require_string(entry.get("artifactDigest"), "artifactDigest")
            if not DIGEST_RE.fullmatch(digest):
                fail(f"baseline artifact digest is not an immutable sha256 digest")

            relative_text = require_string(entry.get("path"), "path")
            relative = PurePosixPath(relative_text)
            if (
                relative.is_absolute()
                or ".." in relative.parts
                or relative.suffix != ".json"
            ):
                fail(f"baseline path must be a safe relative JSON path: {relative_text}")
            if not relative.parts or relative.parts[0] != set_id:
                fail(f"baseline path must live under {set_id}: {relative_text}")
            path = (root / Path(relative)).resolve()
            if root not in path.parents or path == index_path:
                fail(f"baseline path escapes its root: {relative_text}")
            if relative_text in indexed_paths:
                fail(f"baseline path is indexed more than once: {relative_text}")
            indexed_paths.add(relative_text)

            selected_cases, outcomes = validate_baseline(path, entry, source_commit)
            if reference_selection is None:
                reference_selection = selected_cases
                reference_outcomes = outcomes
            elif selected_cases != reference_selection:
                fail(f"{path}: selected scenarios differ inside baseline set {set_id}")
            elif outcomes != reference_outcomes:
                fail(f"{path}: diagnostic root outcomes differ inside baseline set {set_id}")
            total_baselines += 1

        if versions != sorted(versions) or len(versions) != len(set(versions)):
            fail(f"baseline versions in {set_id} must be unique and sorted")

    if len(set_ids) != len(set(set_ids)):
        fail("baseline set IDs must be unique")
    if workflow_runs != sorted(workflow_runs) or len(workflow_runs) != len(
        set(workflow_runs)
    ):
        fail("baseline workflow runs must be unique and sorted")

    actual_paths = {
        path.relative_to(root).as_posix()
        for path in root.rglob("*.json")
        if path.resolve() != index_path.resolve()
    }
    if actual_paths != indexed_paths:
        missing = sorted(indexed_paths - actual_paths)
        unindexed = sorted(actual_paths - indexed_paths)
        fail(f"baseline file inventory mismatch; missing={missing}, unindexed={unindexed}")

    return {"sets": len(sets), "baselines": total_baselines}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("root", type=Path)
    args = parser.parse_args()
    try:
        result = validate(args.root)
    except (ValueError, OSError) as error:
        print(f"validate-e2e-baselines: {error}", file=sys.stderr)
        raise SystemExit(1) from error
    print(
        "validated "
        f"{result['baselines']} compatibility baselines in {result['sets']} set(s)"
    )


if __name__ == "__main__":
    main()
