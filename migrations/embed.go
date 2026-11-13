// Package migrations embeds the SQL migrations; they are applied at startup.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
