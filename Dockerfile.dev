# Dev image: source is bind-mounted over /app by docker-compose, so this just
# provides the toolchain plus a warm module cache. Production images are
# Phase 10 (see docs/DEPLOY.md).
FROM golang:1.26-alpine
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
EXPOSE 8080
CMD ["go", "run", "./cmd/api"]
