.PHONY: help unit-test e2e-test

help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*##' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-15s %s\n", $$1, $$2}'

unit-test: ## Run unit tests with coverage
	go test -coverprofile=coverage-unit.out ./...
	go tool cover -func=coverage-unit.out

E2E_COMPOSE = docker compose -f e2e/compose.yaml

e2e-test: ## Run e2e tests against the container in docker compose
	$(E2E_COMPOSE) up --build --wait gitserver git-mcp || { $(E2E_COMPOSE) logs; $(E2E_COMPOSE) down -v; exit 1; }
	$(E2E_COMPOSE) run --rm --use-aliases tester; status=$$?; \
	  [ $$status -eq 0 ] || $(E2E_COMPOSE) logs git-mcp; \
	  $(E2E_COMPOSE) down -v; exit $$status
