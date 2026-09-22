// Package migrations embeds the SQL migration files so they ship inside the
// backend binary - no separate file mount needed to run them on boot.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
