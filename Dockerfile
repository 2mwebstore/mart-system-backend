# Production image: a static Go binary. Migrations are embedded in it and run
# on start (AUTO_MIGRATE=true, the default). The listen port follows $PORT
# (set by Railway) unless APP_PORT is given.
#
# `seed` and `superadmin` are in the image too, so they can be run inside
# Railway's network, where the private database host resolves.
# APP_ENV=production below makes `seed` create the super admin only (no
# companies, no demo data), and it needs SEED_SUPERADMIN_PASSWORD.
# `superadmin` creates or resets a super admin on a running install, from
# SUPERADMIN_USERNAME and SUPERADMIN_PASSWORD (see docs/DEPLOY.md).
#
# The dev image (`go run`, source bind-mounted by docker-compose) is Dockerfile.dev.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed ./seed \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/superadmin ./cmd/superadmin

FROM alpine:3.20
# tzdata: APP_TIMEZONE (Asia/Phnom_Penh) needs the zoneinfo database.
# mariadb-client: provides `mysqldump`, for the Settings > Alerts & Backup
# "Backup now" button and the nightly scheduler (internal/services/backup_service.go).
# mariadb-connector-c: the client's sign-in plugins. MySQL 8 and 9 sign users
# in with caching_sha2_password, and Alpine's mariadb-client ships without it,
# so without this every dump fails ("Plugin caching_sha2_password could not be
# loaded"). tests/backup_image_test.sh checks a dump from the built image.
RUN apk add --no-cache ca-certificates tzdata mariadb-client mariadb-connector-c \
 && adduser -D -H -u 10001 app \
 && mkdir -p /app/backups \
 && chown -R app:app /app
COPY --from=build /out/api /out/seed /out/superadmin /usr/local/bin/
ENV APP_ENV=production
# The default BACKUP_DIR (./backups) resolves under here. A mounted volume is
# owned by root, so see docs/DEPLOY.md if BACKUP_DIR points at one.
WORKDIR /app
USER app
EXPOSE 8080
CMD ["api"]
