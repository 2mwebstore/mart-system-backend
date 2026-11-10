-include ../.env
export

# Everything the integration tests need, none of it shared with the dev database.
TEST_DB_CONTAINER=com_mart_test_mysql
TEST_DB_PORT=3399
TEST_DB_NAME=com_mart_test
TEST_DB_USER=com_mart_test
TEST_DB_PASSWORD=test-only-password

MIGRATE_DSN=mysql://$(DB_USER):$(DB_PASSWORD)@tcp($(DB_HOST):$(DB_PORT))/$(DB_NAME)?charset=utf8mb4&collation=utf8mb4_unicode_ci&multiStatements=true

.PHONY: run build test test-integration vet migrate-up migrate-down migrate-down-all migrate-new seed

run:
	go run ./cmd/api

build:
	go build -o bin/api ./cmd/api

# Unit tests. The integration tests skip here: they only run against a database
# whose name ends in _test, so this can't touch the dev database.
test:
	go test ./...

# Everything, against a throwaway MySQL that's deleted afterwards.
test-integration:
	@docker rm -f $(TEST_DB_CONTAINER) >/dev/null 2>&1 || true
	@docker run -d --name $(TEST_DB_CONTAINER) --tmpfs /var/lib/mysql \
		-e MYSQL_DATABASE=$(TEST_DB_NAME) -e MYSQL_USER=$(TEST_DB_USER) \
		-e MYSQL_PASSWORD=$(TEST_DB_PASSWORD) -e MYSQL_ROOT_PASSWORD=$(TEST_DB_PASSWORD) \
		-p 127.0.0.1:$(TEST_DB_PORT):3306 mysql:8.4 >/dev/null
	@echo "waiting for MySQL..."
	@until docker exec -e MYSQL_PWD=$(TEST_DB_PASSWORD) $(TEST_DB_CONTAINER) \
		mysql -h127.0.0.1 -u$(TEST_DB_USER) $(TEST_DB_NAME) -e 'SELECT 1' >/dev/null 2>&1; do sleep 1; done
	@status=0; DB_HOST=127.0.0.1 DB_PORT=$(TEST_DB_PORT) DB_NAME=$(TEST_DB_NAME) DB_USER=$(TEST_DB_USER) \
		DB_PASSWORD=$(TEST_DB_PASSWORD) APP_ENV=test REQUIRE_TEST_DB=1 \
		go test ./... -count=1 || status=$$?; \
		docker rm -f $(TEST_DB_CONTAINER) >/dev/null; exit $$status

vet:
	go vet ./...

migrate-up:
	migrate -database "$(MIGRATE_DSN)" -path migrations up

migrate-down:
	migrate -database "$(MIGRATE_DSN)" -path migrations down 1

migrate-down-all:
	migrate -database "$(MIGRATE_DSN)" -path migrations down -all

migrate-new:
	migrate create -ext sql -dir migrations -seq $(name)

seed:
	go run ./seed
