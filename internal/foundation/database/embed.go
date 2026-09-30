package database

import "embed"

//go:embed migrations/*.sql migrations-postgres/*.sql
var coreMigrations embed.FS
