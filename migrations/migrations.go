package migrations

import "embed"

// FS carries the forward-only migration set applied in filename order.
//
//go:embed *.sql
var FS embed.FS
