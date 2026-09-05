#!/usr/bin/env bash
# Verify and smoke-test one native binary from an evaluator snapshot artifact.
set -euo pipefail

die() {
  printf 'verify-evaluator-snapshot: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: ./scripts/verify-evaluator-snapshot.sh ARTIFACT_DIR SNAPSHOT_ID GOOS GOARCH [SOURCE_COMMIT]

Verify the complete four-binary evaluator artifact set, confirm that this host
matches GOOS/GOARCH, and execute the matching binary's --version command.
When SOURCE_COMMIT is provided, also require a clean build from that commit.
EOF
}

if [[ "$#" -lt 4 || "$#" -gt 5 ]]; then
  usage >&2
  exit 2
fi

ARTIFACT_DIR="${1%/}"
SNAPSHOT_ID="$2"
EXPECTED_GOOS="$3"
EXPECTED_GOARCH="$4"
EXPECTED_SOURCE_COMMIT="${5:-}"

[[ -d "$ARTIFACT_DIR" && ! -L "$ARTIFACT_DIR" ]] || \
  die "artifact directory is missing or is a symlink: ${ARTIFACT_DIR}"
[[ "$SNAPSHOT_ID" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$ ]] || \
  die "unsafe snapshot identity: ${SNAPSHOT_ID}"

case "$EXPECTED_GOOS/$EXPECTED_GOARCH" in
  linux/amd64|linux/arm64|darwin/amd64|darwin/arm64) ;;
  *) die "unsupported target: ${EXPECTED_GOOS}/${EXPECTED_GOARCH}" ;;
esac

ARTIFACTS=(
  "kubectl-tuna-${SNAPSHOT_ID}-linux-amd64"
  "kubectl-tuna-${SNAPSHOT_ID}-linux-arm64"
  "kubectl-tuna-${SNAPSHOT_ID}-darwin-amd64"
  "kubectl-tuna-${SNAPSHOT_ID}-darwin-arm64"
)
METADATA=(SHA256SUMS NOTICE.txt BUILD-IDENTITY.txt)

entry_count="$(find "$ARTIFACT_DIR" -mindepth 1 -maxdepth 1 | wc -l | tr -d '[:space:]')"
[[ "$entry_count" == "7" ]] || die "expected 7 artifact entries, found ${entry_count}"

for entry in "${ARTIFACTS[@]}" "${METADATA[@]}"; do
  [[ -f "$ARTIFACT_DIR/$entry" && ! -L "$ARTIFACT_DIR/$entry" ]] || \
    die "missing, non-regular, or symlinked artifact entry: ${entry}"
done

checksum_count="$(wc -l < "$ARTIFACT_DIR/SHA256SUMS" | tr -d '[:space:]')"
[[ "$checksum_count" == "4" ]] || \
  die "expected 4 checksum entries, found ${checksum_count}"
for artifact in "${ARTIFACTS[@]}"; do
  checksum_matches="$(awk -v name="$artifact" '$2 == name { count++ } END { print count + 0 }' \
    "$ARTIFACT_DIR/SHA256SUMS")"
  [[ "$checksum_matches" == "1" ]] || \
    die "checksum manifest must contain ${artifact} exactly once"
done

(
  cd "$ARTIFACT_DIR"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c SHA256SUMS
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 -c SHA256SUMS
  else
    die "neither sha256sum nor shasum is available"
  fi
)

grep -Fqx "snapshot_id=${SNAPSHOT_ID}" "$ARTIFACT_DIR/BUILD-IDENTITY.txt" || \
  die "build identity does not match snapshot ID"
grep -Fqx 'label=unreleased/unsupported/unsigned' "$ARTIFACT_DIR/BUILD-IDENTITY.txt" || \
  die "build identity is missing the unsupported label"
grep -Fqx 'targets=linux/amd64 linux/arm64 darwin/amd64 darwin/arm64' \
  "$ARTIFACT_DIR/BUILD-IDENTITY.txt" || die "build identity target list is incomplete"
grep -Fq 'unreleased / unsupported / unsigned' "$ARTIFACT_DIR/NOTICE.txt" || \
  die "notice is missing the unsupported label"

if [[ -n "$EXPECTED_SOURCE_COMMIT" ]]; then
  grep -Fqx "source_commit=${EXPECTED_SOURCE_COMMIT}" "$ARTIFACT_DIR/BUILD-IDENTITY.txt" || \
    die "build identity does not match source commit"
  grep -Fqx 'source_dirty=false' "$ARTIFACT_DIR/BUILD-IDENTITY.txt" || \
    die "CI artifact was not built from a clean source tree"
fi

case "$(uname -s)" in
  Linux) HOST_GOOS="linux" ;;
  Darwin) HOST_GOOS="darwin" ;;
  *) die "unsupported host operating system: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64) HOST_GOARCH="amd64" ;;
  arm64|aarch64) HOST_GOARCH="arm64" ;;
  *) die "unsupported host architecture: $(uname -m)" ;;
esac

[[ "$HOST_GOOS" == "$EXPECTED_GOOS" ]] || \
  die "runner OS ${HOST_GOOS} does not match expected ${EXPECTED_GOOS}"
[[ "$HOST_GOARCH" == "$EXPECTED_GOARCH" ]] || \
  die "runner architecture ${HOST_GOARCH} does not match expected ${EXPECTED_GOARCH}"

BINARY="$ARTIFACT_DIR/kubectl-tuna-${SNAPSHOT_ID}-${EXPECTED_GOOS}-${EXPECTED_GOARCH}"
chmod +x "$BINARY"
actual_version="$("$BINARY" --version)"
expected_version="kubectl-tuna version evaluator-snapshot/${SNAPSHOT_ID}"
[[ "$actual_version" == "$expected_version" ]] || \
  die "unexpected version output: ${actual_version}"

printf 'verified evaluator snapshot %s on %s/%s\n' \
  "$SNAPSHOT_ID" "$HOST_GOOS" "$HOST_GOARCH"
