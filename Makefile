.PHONY: all fmt vet lint sec test check build install clean

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
sec:
	gosec -severity medium -concurrency 1 -quiet -exclude=G304,G301,G306 ./...

test:
	go test -race -count=1 ./...

build:
	go build -o obconverge ./cmd/obconverge

install:
	go install ./cmd/obconverge

clean:
	rm -f obconverge
	go clean -testcache
