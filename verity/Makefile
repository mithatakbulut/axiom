VERSION ?= 0.1.0
GO ?= go
DIST ?= dist

.PHONY: test vet build package benchmark release-check clean

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

build:
	$(GO) build ./...

package:
	mkdir -p $(DIST)
	CGO_ENABLED=1 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(DIST)/verity ./cmd/verity
	cd $(DIST) && tar -czf verity_$(VERSION)_$$(go env GOOS)_$$(go env GOARCH).tar.gz verity

benchmark:
	$(GO) test ./internal/proof -run '^$$' -bench BenchmarkArtifactStore -benchmem

release-check: test vet build

clean:
	rm -rf $(DIST)
