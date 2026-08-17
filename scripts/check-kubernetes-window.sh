#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
manifest="${root_dir}/testdata/compatibility/kubernetes-window.json"

for command_name in curl docker python3; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "required command not found: ${command_name}" >&2
    exit 1
  fi
done

entries="$({
  python3 - "${manifest}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    manifest = json.load(stream)

if manifest.get("schemaVersion") != 1:
    raise SystemExit("unsupported Kubernetes window schema")

for entry in manifest.get("entries", []):
    print("\t".join([
        entry["minor"],
        entry["upstreamLatestPatch"],
        entry["testedPatch"],
        entry["nodeImage"],
        str(entry["imageLag"]).lower(),
    ]))
PY
})"

if [[ -z "${entries}" ]]; then
  echo "Kubernetes window has no entries" >&2
  exit 1
fi

while IFS=$'\t' read -r minor recorded_upstream tested_patch node_image image_lag; do
  current_upstream="$(curl --fail --silent --show-error --location "https://dl.k8s.io/release/stable-${minor}.txt")"
  current_upstream="${current_upstream#v}"

  if [[ "${current_upstream}" != "${recorded_upstream}" ]]; then
    echo "Kubernetes ${minor} drift: manifest records ${recorded_upstream}, upstream reports ${current_upstream}" >&2
    echo "Review the release and update testdata/compatibility/kubernetes-window.json." >&2
    exit 1
  fi

  if ! docker manifest inspect "${node_image}" >/dev/null 2>&1; then
    echo "Pinned Kind image cannot be resolved: ${node_image}" >&2
    exit 1
  fi

  if [[ "${tested_patch}" == "${recorded_upstream}" ]]; then
    if [[ "${image_lag}" != "false" ]]; then
      echo "Kubernetes ${minor}: imageLag must be false when tested and upstream patches match" >&2
      exit 1
    fi
    echo "Kubernetes ${minor}: tested patch ${tested_patch} matches upstream and its pinned image resolves"
    continue
  fi

  if [[ "${image_lag}" != "true" ]]; then
    echo "Kubernetes ${minor}: imageLag must acknowledge tested ${tested_patch} versus upstream ${recorded_upstream}" >&2
    exit 1
  fi

  tested_patch_number="${tested_patch##*.}"
  upstream_patch_number="${recorded_upstream##*.}"
  for ((patch_number = 10#${upstream_patch_number}; patch_number > 10#${tested_patch_number}; patch_number--)); do
    candidate_image="kindest/node:v${minor}.${patch_number}"
    candidate_error=""
    if candidate_error="$(docker manifest inspect "${candidate_image}" 2>&1)"; then
      echo "Kubernetes ${minor}: newer ${candidate_image} exists; pin its digest and advance the e2e matrix" >&2
      exit 1
    fi
    case "${candidate_error}" in
      *"no such manifest"*|*"manifest unknown"*) ;;
      *)
        echo "Could not determine whether ${candidate_image} exists; registry check failed unexpectedly" >&2
        exit 1
        ;;
    esac
  done

  echo "Kubernetes ${minor}: upstream is ${recorded_upstream}; latest resolvable pinned test image remains ${tested_patch} (acknowledged image lag)"
done <<< "${entries}"
