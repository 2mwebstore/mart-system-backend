include ../.env
export

MIGRATE_DSN=mysql://$(DB_USER):$(DB_PASSWORD)@tcp($(DB_HOST):$(DB_PORT))/$(DB_NAME)?charset=utf8mb4&collation=utf8mb4_unicode_ci&multiStatements=true

.PHONY: run build test vet migrate-up migrate-down migrate-down-all migrate-new seed

run:
	go run ./cmd/api

build:
	go build -o bin/api ./cmd/api

test:
	go test ./... -v

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
