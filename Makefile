.PHONY: build test vet e2e demo install-plugin release-snapshot demo-gif

PREFIX ?= $(HOME)/.local
BIN_DIR ?= $(PREFIX)/bin

build:
	go build -o bin/kdiag ./cmd/kdiag
	go build -o bin/kubectl-diag ./cmd/kdiag

test:
	go test ./...

vet:
	go vet ./...

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

# Regenerate docs/demo.gif (requires python3 + pillow)
demo-gif:
	python3 -m venv .venv-gif
	.venv-gif/bin/pip -q install pillow
	.venv-gif/bin/python scripts/generate-demo-gif.py
