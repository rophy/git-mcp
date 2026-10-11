.PHONY: help unit-test e2e-test

help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*##' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-15s %s\n", $$1, $$2}'

unit-test: ## Run unit tests with coverage
	go test -coverprofile=coverage-unit.out ./...
	go tool cover -func=coverage-unit.out

e2e-test: ## Run e2e tests with coverage
	go test -tags=integration -coverprofile=coverage-e2e.out ./...
	go tool cover -func=coverage-e2e.out
