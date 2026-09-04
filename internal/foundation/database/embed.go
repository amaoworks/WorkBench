package database

import "embed"

//go:embed migrations/*.sql
var coreMigrations embed.FS
