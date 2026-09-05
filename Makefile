.PHONY: build test vet verify benchmark corpus e2e e2e-full e2e-list e2e-selection-test e2e-baseline-test check-kubernetes-window demo install-plugin demo-output demo-gif evaluator-snapshot

PREFIX ?= $(HOME)/.local
BIN_DIR ?= $(PREFIX)/bin
SNAPSHOT_ID ?= unreleased

build:
	go build -o bin/kubectl-tuna ./cmd/kubectl-tuna

# Local unreleased/unsupported evaluator binaries under dist/ (not a release).
# Override identity with: make evaluator-snapshot SNAPSHOT_ID=safe-id
# Refuses an existing final output path; see scripts/build-evaluator-snapshot.sh.
evaluator-snapshot:
	bash ./scripts/build-evaluator-snapshot.sh "$(SNAPSHOT_ID)"

test:
	go test ./...

vet:
	go vet ./...

# Fast, cluster-free validation for normal local development.
verify: test vet
	bash -n examples/e2e.sh scripts/*.sh
	bash ./scripts/test-e2e-selection.sh
	python3 -m unittest scripts/test_summarize_e2e_baseline.py

# Local collector cost as namespace Service count grows. This benchmark uses
# client-go's fake tracker, so it measures decode/filter/graph overhead rather
# than API-server or network latency.
benchmark:
	go test ./internal/kube -run '^$$' -bench BenchmarkCollectPodServiceNamespace -benchmem

# Validate every labeled seed-corpus case through the collector and engine.
corpus:
	go test ./internal/diag -run '^TestSeedCorpusExpectations$$' -v

# Install as a kubectl plugin on PATH → `kubectl tuna …`
install-plugin: build
	mkdir -p "$(BIN_DIR)"
	install -m 755 bin/kubectl-tuna "$(BIN_DIR)/kubectl-tuna"
	@echo "installed $(BIN_DIR)/kubectl-tuna"
	@echo "ensure $(BIN_DIR) is on PATH, then: kubectl tuna --help"

# Requires kubectl pointing at a disposable cluster. Select exactly one case:
# make e2e E2E_CASE=healthy-service
e2e:
	@test -n "$(E2E_CASE)" || (echo "set E2E_CASE; run 'make e2e-list' for choices" >&2; exit 2)
	./examples/e2e.sh "$(E2E_CASE)"

# Explicit milestone-only local run. Normal development must use targeted e2e.
e2e-full:
	./examples/e2e.sh --all

e2e-list:
	@./examples/e2e.sh --list

e2e-selection-test:
	bash ./scripts/test-e2e-selection.sh

e2e-baseline-test:
	python3 -m unittest scripts/test_summarize_e2e_baseline.py

# Requires network access. Checks upstream patch and Kind image drift.
check-kubernetes-window:
	./scripts/check-kubernetes-window.sh

# make demo SCENARIO=broken-readiness-port
demo:
	./examples/run-demo.sh $(SCENARIO)

# Regenerate the deterministic fixture report used by the GIF.
demo-output:
	./scripts/render-demo.sh

# Regenerate docs/demo.gif (requires python3; installs pinned Pillow in an ignored venv)
demo-gif: demo-output
	python3 -m venv .venv-gif
	.venv-gif/bin/pip -q install pillow==12.2.0
	.venv-gif/bin/python scripts/generate-demo-gif.py
