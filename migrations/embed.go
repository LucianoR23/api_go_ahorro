// Package migrations embeds the golang-migrate SQL files into the binary so the
// API can apply them on startup (MIGRATE_ON_START) without shipping the folder
// or the migrate CLI in the image. The files stay the source of truth for the
// CLI workflow in local dev too.
package migrations

import "embed"

// FS holds every *.up.sql / *.down.sql file in this directory.
//
//go:embed *.sql
var FS embed.FS
