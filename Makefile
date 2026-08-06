.PHONY: build test vet benchmark corpus e2e demo install-plugin release-snapshot demo-output demo-gif

PREFIX ?= $(HOME)/.local
BIN_DIR ?= $(PREFIX)/bin

build:
	go build -o bin/kdiag ./cmd/kdiag
	go build -o bin/kubectl-diag ./cmd/kdiag

test:
	go test ./...

vet:
	go vet ./...

# Local collector cost as namespace Service count grows. This benchmark uses
# client-go's fake tracker, so it measures decode/filter/graph overhead rather
# than API-server or network latency.
benchmark:
	go test ./internal/kube -run '^$$' -bench BenchmarkCollectPodServiceNamespace -benchmem

# Validate every labeled seed-corpus case through the collector and engine.
corpus:
	go test ./internal/diag -run '^TestSeedCorpusExpectations$$' -v

# Install as a kubectl plugin on PATH → `kubectl diag …`
install-plugin: build
	mkdir -p "$(BIN_DIR)"
	install -m 755 bin/kubectl-diag "$(BIN_DIR)/kubectl-diag"
	@echo "installed $(BIN_DIR)/kubectl-diag"
	@echo "ensure $(BIN_DIR) is on PATH, then: kubectl diag --help"

# Requires kubectl pointing at a disposable cluster (e.g. kind create cluster)
e2e:
	./examples/e2e.sh

# make demo SCENARIO=broken-readiness-port
demo:
	./examples/run-demo.sh $(SCENARIO)

# Snapshot release artifacts + generated krew manifest (dist/diag.yaml)
release-snapshot:
	goreleaser release --snapshot --clean

# Regenerate the deterministic fixture report used by the GIF.
demo-output:
	./scripts/render-demo.sh

# Regenerate docs/demo.gif (requires python3; installs pinned Pillow in an ignored venv)
demo-gif: demo-output
	python3 -m venv .venv-gif
	.venv-gif/bin/pip -q install pillow==12.2.0
	.venv-gif/bin/python scripts/generate-demo-gif.py
