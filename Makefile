GO ?= go

.PHONY: test race cover vet fmt demo

test:
	$(GO) test -v -count=1 ./...

race:
	CGO_ENABLED=1 $(GO) test -race -count=20 ./...

cover:
	$(GO) test -coverprofile=cov.out ./...
	$(GO) tool cover -func=cov.out

vet:
	$(GO) vet ./...

fmt:
	gofmt -l .

demo:
	$(GO) run ./cmd/lre
