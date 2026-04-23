.PHONY: all fmt vet lint sec test check build install clean

# Version string stamped into the binary via -ldflags. Override at the CLI:
#   make install VERSION=v0.2.0
VERSION ?= v0.1.0-audit
LDFLAGS := -X main.version=$(VERSION)

# Single canonical check target — CI / pre-merge must pass this.
all: check

check: fmt vet lint sec test

fmt:
	@gofmt -l . | tee /tmp/obconverge.gofmt.out; test ! -s /tmp/obconverge.gofmt.out

vet:
	go vet ./...

lint:
	golangci-lint run ./...

# gosec exclusions — justified by the tool's nature:
#   G304  (Potential file inclusion via variable): obconverge operates on
#         operator-supplied filesystem paths by design. The threat model is a
#         local CLI invoked by the vault owner, not an untrusted network input.
#   G301  (Directory created with 0o755): standard perms for user-facing dirs,
#         matches Obsidian's own behavior for vault subdirectories.
#   G306  (WriteFile with 0o644): standard perms for user-readable output
#         artifacts; vault notes themselves are typically 0o644.
#   G122  (filepath.WalkDir callback uses race-prone path): obconverge walks
#         the operator's local vault. TOCTOU between directory entry and
#         ReadFile is acceptable for an offline auditor where the operator
#         owns the filesystem; if a file is replaced mid-walk, re-running
#         the scan is the answer. The os.Root API would change the read
#         semantics in ways that matter for apply (future commit) more
#         than it does here.
#   G703  (path traversal via taint analysis): same class as G304. apply
#         writes to operator-supplied vault paths by design; the atomic
#         temp-then-rename pattern writes to "<vaultfile>.obconverge.tmp"
#         immediately adjacent to the real file.
sec:
	gosec -severity medium -concurrency 1 -quiet -exclude=G304,G301,G306,G122,G703 ./...

test:
	go test -race -count=1 ./...

build:
	go build -ldflags "$(LDFLAGS)" -o obconverge ./cmd/obconverge

install:
	rm -f "$$(go env GOPATH)/bin/obconverge"
	go install -ldflags "$(LDFLAGS)" ./cmd/obconverge

clean:
	rm -f obconverge
	go clean -testcache
