// Package migrations embeds the SQL migration files so the server binary can
// self-migrate at startup (golang-migrate iofs source).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
