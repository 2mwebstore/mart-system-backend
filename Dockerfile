# Production image: a static Go binary. Migrations are embedded in it and run
# on start (AUTO_MIGRATE=true, the default). The listen port follows $PORT
# (set by Railway) unless APP_PORT is given.
#
# `seed` (base data + demo data) is in the image too, so it can be run inside
# Railway's network, where the private database host resolves.
#
# The dev image (`go run`, source bind-mounted by docker-compose) is Dockerfile.dev.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed ./seed

FROM alpine:3.20
# tzdata: APP_TIMEZONE (Asia/Phnom_Penh) needs the zoneinfo database.
# mariadb-client: provides `mysqldump`, for the Settings > Alerts & Backup
# "Backup now" button and the nightly scheduler (internal/services/backup_service.go).
RUN apk add --no-cache ca-certificates tzdata mariadb-client
COPY --from=build /out/api /out/seed /usr/local/bin/
ENV APP_ENV=production
EXPOSE 8080
CMD ["api"]
