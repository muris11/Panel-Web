package db

import (
	"testing"
)

func TestSplitStatements(t *testing.T) {
	statements := SplitStatements(`
-- a comment with a semicolon; inside it
CREATE TABLE IF NOT EXISTS a (id INT);

/* a block comment; with a semicolon */
CREATE TABLE b (note VARCHAR(50) DEFAULT 'a;b');

INSERT INTO b (note) VALUES ("also; fine");
`)

	if len(statements) != 3 {
		t.Fatalf("split into %d statements, want 3: %#v", len(statements), statements)
	}

	if statements[1] != "CREATE TABLE b (note VARCHAR(50) DEFAULT 'a;b')" {
		t.Fatalf("a semicolon inside a string ended the statement: %q", statements[1])
	}

	for _, statement := range statements {
		if statement == "" {
			t.Error("a split produced an empty statement")
		}

		if len(statement) > 2 && statement[0] == '-' {
			t.Errorf("a comment survived as a statement: %q", statement)
		}
	}
}

func TestSplitStatementsKeepsBackticks(t *testing.T) {
	statements := SplitStatements("CREATE TABLE `a;b` (id INT);\nSELECT 1;")

	if len(statements) != 2 {
		t.Fatalf("split into %d statements, want 2: %#v", len(statements), statements)
	}

	if statements[0] != "CREATE TABLE `a;b` (id INT)" {
		t.Fatalf("a semicolon inside a backticked name ended the statement: %q", statements[0])
	}
}
