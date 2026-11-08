# Development tasks. They all run in Docker: nothing is installed on the host.
#   make lint          golangci-lint (src/backend/.golangci.yml) and ESLint (src/web/eslint.config.js)
#   make lint-fix      the same, and repair what the tools can
#   make test          the Go tests
#   make schema        regenerate src/backend/db/schema from db/migrations on a disposable PostgreSQL
#   make schema-check  fail if db/schema does not match the migrations
.PHONY: lint lint-fix test schema schema-check

CACHE := $(or $(XDG_CACHE_HOME),$(HOME)/.cache)/moodle-autograder
USER_ID := $(shell id -u):$(shell id -g)
GO_RUN := -u $(USER_ID) -e HOME=/tmp -e GOCACHE=/cache/go-build -e GOMODCACHE=/cache/mod -e GOLANGCI_LINT_CACHE=/cache/lint -v $(CACHE):/cache -v $(CURDIR):/repo -w /repo/src/backend
WEB_RUN := -u $(USER_ID) -e HOME=/tmp -e npm_config_update_notifier=false -v $(CURDIR)/src/web:/w -w /w

lint lint-fix:
	@mkdir -p $(CACHE)
	docker run --rm $(GO_RUN) golangci/golangci-lint:v2.15.0 golangci-lint run $(if $(filter lint-fix,$@),--fix) ./...
	docker run --rm $(WEB_RUN) node:lts sh -c '[ -d node_modules ] || npm ci --no-audit --no-fund; npx eslint . $(if $(filter lint-fix,$@),--fix)'

test:
	@mkdir -p $(CACHE)
	docker run --rm $(GO_RUN) golang:1.27 go test ./...

# The server answers on TCP only once it has really started. Never touches a real database.
schema schema-check:
	@mkdir -p $(CACHE); net=autograder-schema-$$$$; pg=$$net-pg; \
	trap 'docker rm -f $$pg >/dev/null 2>&1; docker network rm $$net >/dev/null 2>&1' EXIT; \
	docker network create $$net >/dev/null; \
	docker run -d --name $$pg --network $$net -e POSTGRES_PASSWORD=schema -e POSTGRES_DB=schema postgres:18-alpine >/dev/null; \
	until docker exec $$pg pg_isready -h 127.0.0.1 -U postgres -d schema -q; do sleep 1; done; \
	docker run --rm --network $$net -e "DATABASE_URL=postgres://postgres:schema@$$pg:5432/schema?sslmode=disable" $(GO_RUN) golang:1.27 go run ./cmd/autograder migrate && \
	docker exec $$pg pg_dump -U postgres -d schema --schema-only --no-owner --no-privileges --exclude-schema=public \
	  | docker run --rm -i $(GO_RUN) golang:1.27 go run ./cmd/dbschema -out db/schema $(if $(filter schema-check,$@),-check)
