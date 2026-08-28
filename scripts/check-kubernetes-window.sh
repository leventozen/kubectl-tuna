#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
manifest="${root_dir}/testdata/compatibility/kubernetes-window.json"
network_timeout_seconds=30

for command_name in curl python3; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "required command not found: ${command_name}" >&2
    exit 1
  fi
done

inspect_kind_image() {
  local image="$1"

  python3 - "${image}" "${network_timeout_seconds}" <<'PY'
import json
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

image = sys.argv[1]
timeout = float(sys.argv[2])
match = re.fullmatch(r"kindest/node:(v[0-9]+\.[0-9]+\.[0-9]+)(?:@sha256:([0-9a-f]{64}))?", image)
if match is None:
    print(f"unsupported Kind image reference: {image}", file=sys.stderr)
    raise SystemExit(2)

tag, expected_digest = match.groups()
scope = urllib.parse.quote("repository:kindest/node:pull")
auth_url = f"https://auth.docker.io/token?service=registry.docker.io&scope={scope}"

try:
    with urllib.request.urlopen(auth_url, timeout=timeout) as response:
        token = json.load(response)["token"]

    request = urllib.request.Request(
        f"https://registry-1.docker.io/v2/kindest/node/manifests/{tag}",
        method="HEAD",
        headers={
            "Authorization": f"Bearer {token}",
            "Accept": ", ".join([
                "application/vnd.oci.image.index.v1+json",
                "application/vnd.docker.distribution.manifest.list.v2+json",
                "application/vnd.oci.image.manifest.v1+json",
                "application/vnd.docker.distribution.manifest.v2+json",
            ]),
        },
    )
    with urllib.request.urlopen(request, timeout=timeout) as response:
        actual_digest = response.headers.get("Docker-Content-Digest", "")
except urllib.error.HTTPError as error:
    if error.code == 404:
        raise SystemExit(4)
    print(f"registry check returned HTTP {error.code}: {image}", file=sys.stderr)
    raise SystemExit(2)
except (OSError, TimeoutError, urllib.error.URLError, ValueError, KeyError) as error:
    print(f"registry check failed: {image}: {error}", file=sys.stderr)
    raise SystemExit(2)

if not actual_digest.startswith("sha256:"):
    print(f"registry returned no content digest: {image}", file=sys.stderr)
    raise SystemExit(2)
if expected_digest is not None and actual_digest != f"sha256:{expected_digest}":
    print(f"registry digest mismatch: {image} resolves to {actual_digest}", file=sys.stderr)
    raise SystemExit(2)
PY
}

entries="$({
  python3 - "${manifest}" <<'PY'
import json
import datetime
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    manifest = json.load(stream)

if manifest.get("schemaVersion") != 1:
    raise SystemExit("unsupported Kubernetes window schema")

today = datetime.datetime.now(datetime.timezone.utc).date()
for entry in manifest.get("entries", []):
    end_of_life = datetime.date.fromisoformat(entry["upstreamEndOfLife"])
    if today > end_of_life:
        raise SystemExit(
            f'Kubernetes {entry["minor"]} reached upstream end of life; review and advance the window'
        )
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

reviewed_max="$(python3 - "${manifest}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    print(json.load(stream)["reviewedRuleWindow"]["max"])
PY
)"
current_stable="$(curl --connect-timeout 10 --max-time "${network_timeout_seconds}" --fail --silent --show-error --location "https://dl.k8s.io/release/stable.txt")"
current_stable="${current_stable#v}"
current_stable_minor="${current_stable%.*}"
if [[ "${current_stable_minor}" != "${reviewed_max}" ]]; then
  echo "Kubernetes minor drift: reviewed maximum is ${reviewed_max}, current stable minor is ${current_stable_minor}" >&2
  echo "Review the new release and advance testdata/compatibility/kubernetes-window.json." >&2
  exit 1
fi

while IFS=$'\t' read -r minor recorded_upstream tested_patch node_image image_lag; do
  current_upstream="$(curl --connect-timeout 10 --max-time "${network_timeout_seconds}" --fail --silent --show-error --location "https://dl.k8s.io/release/stable-${minor}.txt")"
  current_upstream="${current_upstream#v}"

  if [[ "${current_upstream}" != "${recorded_upstream}" ]]; then
    echo "Kubernetes ${minor} drift: manifest records ${recorded_upstream}, upstream reports ${current_upstream}" >&2
    echo "Review the release and update testdata/compatibility/kubernetes-window.json." >&2
    exit 1
  fi

  if ! inspect_kind_image "${node_image}"; then
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
    if inspect_kind_image "${candidate_image}"; then
      echo "Kubernetes ${minor}: newer ${candidate_image} exists; pin its digest and advance the e2e matrix" >&2
      exit 1
    else
      inspect_status=$?
    fi
    if [[ "${inspect_status}" -ne 4 ]]; then
      echo "Could not determine whether ${candidate_image} exists" >&2
      exit 1
    fi
  done

  echo "Kubernetes ${minor}: upstream is ${recorded_upstream}; latest resolvable pinned test image remains ${tested_patch} (acknowledged image lag)"
done <<< "${entries}"
