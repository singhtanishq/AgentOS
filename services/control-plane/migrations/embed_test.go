package migrations

import "testing"

func TestAllReturnsSortedMigrations(t *testing.T) {
	ms, err := All()
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if len(ms) < 9 {
		t.Fatalf("got %d migrations, want at least 9", len(ms))
	}
	for i := 1; i < len(ms); i++ {
		if ms[i].Version <= ms[i-1].Version {
			t.Fatalf("migrations not sorted: %d then %d", ms[i-1].Version, ms[i].Version)
		}
		if ms[i].Name == "" {
			t.Errorf("migration %d has empty name", ms[i].Version)
		}
		if ms[i].SQL == "" {
			t.Errorf("migration %d has empty SQL", ms[i].Version)
		}
	}
}

func TestSplitVersionName(t *testing.T) {
	cases := []struct {
		in      string
		version int64
		name    string
		ok      bool
	}{
		{"000123_create_agents", 123, "create_agents", true},
		{"000009_rls", 9, "rls", true},
		{"notamigration", 0, "", false},
		{"abc_def", 0, "", false},
	}
	for _, c := range cases {
		v, name, ok := splitVersionName(c.in)
		if ok != c.ok || v != c.version || name != c.name {
			t.Errorf("splitVersionName(%q) = (%d, %q, %v), want (%d, %q, %v)",
				c.in, v, name, ok, c.version, c.name, c.ok)
		}
	}
}
