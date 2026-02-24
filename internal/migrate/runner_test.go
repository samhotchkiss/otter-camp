package migrate

import "testing"

func TestRequiresNonTransactionalExecution(t *testing.T) {
	t.Run("marker present", func(t *testing.T) {
		sql := `
CREATE TABLE example(id integer);
-- HNSW_INDEX: must run outside transaction; migration runner handles this automatically
CREATE INDEX CONCURRENTLY example_idx ON example (id);
`
		if !requiresNonTransactionalExecution(sql) {
			t.Fatal("requiresNonTransactionalExecution = false, want true")
		}
	})

	t.Run("marker missing", func(t *testing.T) {
		sql := `
CREATE TABLE example(id integer);
CREATE INDEX example_idx ON example (id);
`
		if requiresNonTransactionalExecution(sql) {
			t.Fatal("requiresNonTransactionalExecution = true, want false")
		}
	})
}

func TestSplitSQLStatements(t *testing.T) {
	sql := `
CREATE TABLE alpha(id integer);
-- comment
CREATE INDEX CONCURRENTLY alpha_idx ON alpha (id);

`
	statements := splitSQLStatements(sql)
	if len(statements) != 2 {
		t.Fatalf("statement count = %d, want 2", len(statements))
	}
}
