// Package migrations embeds the *.sql migration files into the API binary so
// the server can apply them on startup regardless of its working directory
// (local `go run`, Docker, a production binary).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
