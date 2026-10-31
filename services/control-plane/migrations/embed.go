// Package migrations embeds the SQL schema migrations so the migrate
// command is a self-contained binary.
package migrations

import (
	"embed"
	"errors"
	"sort"
	"strings"
)

//go:embed *.sql
var files embed.FS

// ErrNoMigrations is returned when no migration files are present.
var ErrNoMigrations = errors.New("migrations: no migration files found")

// Migration is one ordered, versioned SQL migration.
type Migration struct {
	Version int64
	Name    string
	SQL     string
}

// All returns every migration sorted by version.
func All() ([]Migration, error) {
	entries, err := files.ReadDir(".")
	if err != nil {
		return nil, err
	}
	var ms []Migration
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		content, err := files.ReadFile(name)
		if err != nil {
			return nil, err
		}
		version, base, ok := splitVersionName(strings.TrimSuffix(name, ".sql"))
		if !ok {
			continue // non-migration file (e.g. README)
		}
		ms = append(ms, Migration{Version: version, Name: base, SQL: string(content)})
	}
	if len(ms) == 0 {
		return nil, ErrNoMigrations
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	return ms, nil
}

// splitVersionName parses "000123_create_agents" into (123, "create_agents").
func splitVersionName(s string) (int64, string, bool) {
	idx := strings.IndexByte(s, '_')
	if idx <= 0 {
		return 0, "", false
	}
	versionPart := s[:idx]
	var v int64
	for _, r := range versionPart {
		if r < '0' || r > '9' {
			return 0, "", false
		}
		v = v*10 + int64(r-'0')
	}
	return v, s[idx+1:], true
}
