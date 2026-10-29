// Package migrations holds the versioned schema migrations in golang-migrate
// format (NNNNNN_name.up.sql / NNNNNN_name.down.sql). The server never applies
// them; run `make migrate-up`. They are embedded so tests can build their schema.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
