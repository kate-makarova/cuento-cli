package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func findReport(t *testing.T, reports []tableReport, name string) tableReport {
	t.Helper()
	for _, r := range reports {
		if r.name == name {
			return r
		}
	}
	t.Fatalf("no report for table %s in %+v", name, reports)
	return tableReport{}
}

func TestCompareSchemas_Status(t *testing.T) {
	t.Parallel()

	schema := `
CREATE TABLE users (
    id int auto_increment primary key,
    username varchar(255) null
);
CREATE TABLE posts (
    id int auto_increment primary key,
    content text not null
);
CREATE TABLE notes (
    id int auto_increment primary key
);`
	live := `
CREATE TABLE users (
    id int auto_increment primary key,
    username varchar(255) null
);
CREATE TABLE notes (
    id int auto_increment primary key,
    legacy varchar(10) null
);
CREATE TABLE old_stuff (
    id int auto_increment primary key
);`

	reports := compareSchemas(schema, live)

	tests := []struct {
		table  string
		status string
	}{
		{"users", tableOK},
		{"posts", tableMissing},
		{"notes", tableDifferences},
		{"old_stuff", tableExtra},
	}
	for _, tt := range tests {
		if got := findReport(t, reports, tt.table).status; got != tt.status {
			t.Errorf("table %s: expected status %q, got %q", tt.table, tt.status, got)
		}
	}

	if last := reports[len(reports)-1]; last.name != "old_stuff" {
		t.Errorf("expected extra tables to come last, got %s", last.name)
	}
	if posts := findReport(t, reports, "posts"); !strings.HasPrefix(posts.schemaSQL, "CREATE TABLE posts") {
		t.Errorf("expected the CREATE statement for a missing table, got %q", posts.schemaSQL)
	}
}

func TestCompareSchemas_ColumnRisk(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		table      string
		schemaCols string
		liveCols   string
		wantRisk   riskLevel
		wantReason string
		wantAlter  string
	}{
		{"new nullable column", "t", "id int primary key,\n    bio text null", "id int primary key", riskLow, "new nullable column", "ALTER TABLE `t` ADD COLUMN"},
		{"new NOT NULL column", "t", "id int primary key,\n    code int not null", "id int primary key", riskMedium, "new NOT NULL column without default", "ALTER TABLE `t` ADD COLUMN"},
		{"text type expanded", "t", "id int primary key,\n    name varchar(255) null", "id int primary key,\n    name varchar(50) null", riskLow, "column type expanded", "ALTER TABLE `t` MODIFY COLUMN"},
		{"text type shrunk", "t", "id int primary key,\n    name varchar(50) null", "id int primary key,\n    name varchar(255) null", riskHigh, "column type shrunk", "ALTER TABLE `t` MODIFY COLUMN"},
		{"column removed", "t", "id int primary key", "id int primary key,\n    old int null", riskHigh, "column to be removed", "ALTER TABLE `t` DROP COLUMN `old`;"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			schema := "CREATE TABLE " + tt.table + " (\n    " + tt.schemaCols + "\n);"
			live := "CREATE TABLE " + tt.table + " (\n    " + tt.liveCols + "\n);"
			r := findReport(t, compareSchemas(schema, live), tt.table)

			if r.status != tableDifferences || len(r.changes) != 1 {
				t.Fatalf("expected one column change, got status %q and %+v", r.status, r.changes)
			}
			if r.changes[0].reason != tt.wantReason {
				t.Errorf("expected reason %q, got %q", tt.wantReason, r.changes[0].reason)
			}
			if r.risk != tt.wantRisk {
				t.Errorf("expected risk %s, got %s", tt.wantRisk.label(), r.risk.label())
			}
			if len(r.alters) != 1 || !strings.HasPrefix(r.alters[0], tt.wantAlter) {
				t.Errorf("expected an alter starting with %q, got %v", tt.wantAlter, r.alters)
			}
		})
	}
}

func TestCompareSchemas_FlattenedIgnoresExtraLiveColumns(t *testing.T) {
	t.Parallel()

	schema := "CREATE TABLE character_flattened (\n    id int primary key\n);"
	live := "CREATE TABLE character_flattened (\n    id int primary key,\n    custom_eyes varchar(255) null\n);"
	if r := findReport(t, compareSchemas(schema, live), "character_flattened"); r.status != tableOK {
		t.Errorf("expected dynamic columns to be ignored, got status %q with %+v", r.status, r.changes)
	}
}

func TestToJSONReport(t *testing.T) {
	t.Parallel()

	reports := []tableReport{
		{name: "users", status: tableOK},
		{name: "posts", status: tableMissing, schemaSQL: "CREATE TABLE posts (id int);"},
		{
			name: "notes", status: tableDifferences, rowCount: 3, risk: riskHigh,
			changes: []colChange{{name: "old", oldDef: "old int null", risk: riskHigh, reason: "column to be removed"}},
			alters:  []string{"ALTER TABLE `notes` DROP COLUMN `old`;"},
		},
		{name: "logs", status: tableDifferences, rowErr: errors.New("timeout"), risk: riskLow},
		{name: "old_stuff", status: tableExtra},
	}

	data, err := json.Marshal(toJSONReport("cuento", reports))
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	var got jsonDiagnosticsReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if got.Database != "cuento" || len(got.Tables) != 5 {
		t.Fatalf("unexpected report: %s", data)
	}
	if posts := got.Tables[1]; len(posts.SQL) != 1 || posts.SQL[0] != "CREATE TABLE posts (id int);" {
		t.Errorf("expected the CREATE statement for the missing table, got %+v", posts)
	}
	notes := got.Tables[2]
	if notes.RowCount == nil || *notes.RowCount != 3 || notes.Risk != "High" || len(notes.Changes) != 1 || notes.Changes[0].Column != "old" {
		t.Errorf("unexpected differences entry: %+v", notes)
	}
	if logs := got.Tables[3]; logs.RowCount != nil {
		t.Errorf("expected no row count when it could not be read, got %d", *logs.RowCount)
	}
	if !strings.Contains(string(data), `"status":"extra"`) {
		t.Errorf("expected the extra table in the output: %s", data)
	}
}
