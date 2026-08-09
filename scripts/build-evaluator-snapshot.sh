#!/usr/bin/env bash
# Build unreleased/unsupported kubectl-tuna evaluator snapshot binaries.
# This is evaluator plumbing, not a release pipeline.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

die() {
  printf 'build-evaluator-snapshot: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: ./scripts/build-evaluator-snapshot.sh [SNAPSHOT_ID]

Build unsigned, unreleased evaluator binaries for linux/darwin on amd64/arm64,
plus SHA-256 checksums and build identity metadata. The default snapshot ID is
"unreleased" and output is written below the ignored dist/ directory.

The final output path must not already exist. A failed build never leaves a
partially complete final artifact set.
EOF
}

if [[ "$#" -gt 1 ]]; then
  usage >&2
  exit 2
fi

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

SNAPSHOT_ID="${1:-${SNAPSHOT_ID:-unreleased}}"
if [[ ! "$SNAPSHOT_ID" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$ ]]; then
  die "unsafe snapshot identity '${SNAPSHOT_ID}': use 1-63 characters from [A-Za-z0-9._-], starting with an alphanumeric character"
fi

OUT_DIR_REL="dist/evaluator-snapshot-${SNAPSHOT_ID}"
OUT_DIR="$ROOT/$OUT_DIR_REL"

if [[ -e "$OUT_DIR" || -L "$OUT_DIR" ]]; then
  die "refusing existing output path: ${OUT_DIR_REL}"
fi

if [[ -L "$ROOT/dist" ]]; then
  die "refusing symlinked output parent: dist"
fi

command -v go >/dev/null 2>&1 || die "go is required on PATH"

if command -v sha256sum >/dev/null 2>&1; then
  write_checksums() { sha256sum "$@"; }
  verify_checksums() { sha256sum -c SHA256SUMS; }
  CHECKSUM_TOOL="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
  write_checksums() { shasum -a 256 "$@"; }
  verify_checksums() { shasum -a 256 -c SHA256SUMS; }
  CHECKSUM_TOOL="shasum -a 256"
else
  die "neither sha256sum nor shasum is available"
fi

GO_VERSION="$(go env GOVERSION)"
MODULE_PATH="$(go list -m -f '{{.Path}}')"

SOURCE_COMMIT="unavailable"
SOURCE_DIRTY="unknown"
if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  SOURCE_COMMIT="$(git rev-parse HEAD)"
  if [[ -n "$(git status --porcelain --untracked-files=all)" ]]; then
    SOURCE_DIRTY="true"
  else
    SOURCE_DIRTY="false"
  fi
fi

mkdir -p "$ROOT/dist"
STAGING_DIR="$(mktemp -d "$ROOT/dist/.evaluator-snapshot-${SNAPSHOT_ID}.XXXXXX")"

cleanup() {
  if [[ -n "${STAGING_DIR:-}" ]]; then
    case "$STAGING_DIR" in
      "$ROOT"/dist/.evaluator-snapshot-*) rm -rf -- "$STAGING_DIR" ;;
      *) printf 'refusing to clean unexpected staging path: %s\n' "$STAGING_DIR" >&2 ;;
    esac
  fi
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM

BINARY_VERSION="evaluator-snapshot/${SNAPSHOT_ID}"
LDFLAGS="-X main.version=${BINARY_VERSION}"
TARGETS=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
)
ARTIFACTS=()

for target in "${TARGETS[@]}"; do
  goos="${target%% *}"
  goarch="${target##* }"
  artifact="kubectl-tuna-${SNAPSHOT_ID}-${goos}-${goarch}"
  printf 'building %s\n' "$artifact"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
    -trimpath \
    -buildvcs=false \
    -ldflags "$LDFLAGS" \
    -o "$STAGING_DIR/$artifact" \
    ./cmd/kubectl-tuna
  ARTIFACTS+=("$artifact")
done

cat >"$STAGING_DIR/NOTICE.txt" <<'EOF'
Tuna evaluator snapshot - unreleased / unsupported / unsigned

These kubectl-tuna binaries are temporary evaluator snapshots for selected
testing. They are not a Tuna release.

- Status: unreleased and unsupported
- Signing: unsigned; do not disable operating-system security controls to run them
- Compatibility: no support, stability, or general-availability promise
- Distribution: not a Krew package, installer, or tagged release

Build from source remains the default evaluator path. Verify SHA256SUMS before
use and include BUILD-IDENTITY.txt with feedback.
EOF

{
  printf 'snapshot_id=%s\n' "$SNAPSHOT_ID"
  printf 'label=unreleased/unsupported/unsigned\n'
  printf 'module=%s\n' "$MODULE_PATH"
  printf 'package=./cmd/kubectl-tuna\n'
  printf 'binary_version=%s\n' "$BINARY_VERSION"
  printf 'go_version=%s\n' "$GO_VERSION"
  printf 'cgo_enabled=0\n'
  printf 'build_flags=-trimpath -buildvcs=false\n'
  printf 'ldflags=%s\n' "$LDFLAGS"
  printf 'source_commit=%s\n' "$SOURCE_COMMIT"
  printf 'source_dirty=%s\n' "$SOURCE_DIRTY"
  printf 'targets=linux/amd64 linux/arm64 darwin/amd64 darwin/arm64\n'
  printf 'artifacts=\n'
  for artifact in "${ARTIFACTS[@]}"; do
    printf '  %s\n' "$artifact"
  done
  printf 'output_dir_basename=%s\n' "$(basename "$OUT_DIR")"
  printf '%s\n' 'reproducibility_note=Same source and Go toolchain may reproduce these artifacts; bit-for-bit equality across different toolchains or hosts is not claimed.'
} >"$STAGING_DIR/BUILD-IDENTITY.txt"

(
  cd "$STAGING_DIR"
  write_checksums "${ARTIFACTS[@]}" >SHA256SUMS
  verify_checksums
)

if [[ -e "$OUT_DIR" || -L "$OUT_DIR" ]]; then
  die "output path appeared during the build: ${OUT_DIR_REL}"
fi
mv "$STAGING_DIR" "$OUT_DIR"
STAGING_DIR=""
trap - EXIT HUP INT TERM

printf 'wrote evaluator snapshot under %s\n' "$OUT_DIR_REL"
printf 'checksum tool: %s\n' "$CHECKSUM_TOOL"
