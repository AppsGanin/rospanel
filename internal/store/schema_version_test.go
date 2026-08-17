package store

import "testing"

func TestSchemaVersionIsSane(t *testing.T) {
	if v := SchemaVersion(); v < 50 {
		t.Errorf("SchemaVersion() = %d, want the newest embedded migration (>=50)", v)
	}
	if n := migrationNumber("0053_full_config_snapshots.sql"); n != 53 {
		t.Errorf("migrationNumber = %d, want 53", n)
	}
}
