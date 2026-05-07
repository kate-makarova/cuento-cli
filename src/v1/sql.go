package main

import (
	"fmt"
	"regexp"
	"strings"
)

// ─── SQL migration generator ──────────────────────────────────────────────────

var reCreateTable = regexp.MustCompile(
	`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?` +
		"(?:`?(\\w+)`?)" +
		`\s*\(([\s\S]+?)\)\s*(?:ENGINE\b|;)`,
)

var reColumnName = regexp.MustCompile("^\\s*`?(\\w+)`?\\s+")
var rePKCols = regexp.MustCompile(`(?i)primary\s+key\s*\(([^)]+)\)`)

// parseTables returns map[tableName]map[columnName]fullColumnDef.
func parseTables(sql string) map[string]map[string]string {
	tables := make(map[string]map[string]string)
	for _, m := range reCreateTable.FindAllStringSubmatch(sql, -1) {
		name := strings.ToLower(m[1])
		cols := make(map[string]string)
		body := m[2]
		var pkCols []string // columns that must be NOT NULL due to PK
		var lastCol string  // last parsed column name (for bare inline "primary key")
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(strings.TrimRight(line, ","))
			if line == "" {
				continue
			}
			upper := strings.ToUpper(line)
			// Bare "primary key" continuation (no parens) — belongs to the
			// column defined on the preceding line.
			if upper == "PRIMARY KEY" {
				if lastCol != "" {
					pkCols = append(pkCols, lastCol)
				}
				continue
			}
			// Skip constraint / index lines. Use word-boundary checks so column
			// names like key_author or index_col are not accidentally skipped.
			if strings.HasPrefix(upper, "PRIMARY ") ||
				strings.HasPrefix(upper, "UNIQUE ") || strings.HasPrefix(upper, "UNIQUE\t") ||
				upper == "KEY" || strings.HasPrefix(upper, "KEY ") || strings.HasPrefix(upper, "KEY\t") || strings.HasPrefix(upper, "KEY`") ||
				strings.HasPrefix(upper, "INDEX ") || strings.HasPrefix(upper, "INDEX\t") ||
				strings.HasPrefix(upper, "CONSTRAINT ") ||
				strings.HasPrefix(upper, "FOREIGN ") {
				continue
			}
			if cm := reColumnName.FindStringSubmatch(line); len(cm) > 1 {
				lastCol = strings.ToLower(cm[1])
				cols[lastCol] = line
			}
		}
		// Also collect PK columns from standalone PRIMARY KEY (...) lines.
		if pm := rePKCols.FindStringSubmatch(strings.ReplaceAll(body, "\n", " ")); pm != nil {
			for _, pkCol := range strings.Split(pm[1], ",") {
				pkCols = append(pkCols, strings.ToLower(strings.Trim(strings.TrimSpace(pkCol), "`")))
			}
		}
		// Primary key columns are implicitly NOT NULL — enforce it.
		for _, pk := range pkCols {
			if def, ok := cols[pk]; ok {
				upper := strings.ToUpper(def)
				if !strings.Contains(upper, "NOT NULL") {
					if strings.Contains(upper, " NULL") {
						def = regexp.MustCompile(`(?i)\bnull\b`).ReplaceAllString(def, "NOT NULL")
					} else {
						def += " NOT NULL"
					}
					cols[pk] = def
				}
			}
		}
		tables[name] = cols
	}
	return tables
}

// extractInserts returns all INSERT statements from sql (single- or multi-line).
// Each returned string is the full statement, whitespace-normalised, up to and
// including the terminating semicolon.
func extractInserts(sql string) []string {
	var inserts []string
	var current strings.Builder
	inInsert := false

	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inInsert {
			if strings.HasPrefix(strings.ToUpper(trimmed), "INSERT") {
				inInsert = true
				current.Reset()
			} else {
				continue
			}
		}
		if current.Len() > 0 {
			current.WriteByte(' ')
		}
		current.WriteString(trimmed)
		if strings.HasSuffix(trimmed, ";") {
			inserts = append(inserts, current.String())
			inInsert = false
		}
	}
	return inserts
}

// generateMigration compares two SQL schemas and returns the migration SQL needed
// to bring the old schema up to the new one (new tables, new columns, new inserts).
func generateMigration(oldSQL, newSQL string) string {
	oldTables := parseTables(oldSQL)
	newTables := parseTables(newSQL)

	var stmts []string

	for tableName, newCols := range newTables {
		oldCols, exists := oldTables[tableName]
		if !exists {
			// Entire table is new — emit the full CREATE TABLE block
			if m := reCreateTable.FindStringSubmatch(newSQL); m != nil {
				for _, mm := range reCreateTable.FindAllStringSubmatch(newSQL, -1) {
					if strings.ToLower(mm[1]) == tableName {
						stmts = append(stmts, mm[0]+";\n")
						break
					}
				}
			}
			continue
		}
		// Table exists — find new or changed columns
		for colName, colDef := range newCols {
			if oldDef, known := oldCols[colName]; !known {
				stmts = append(stmts,
					fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN %s;\n", tableName, colDef))
			} else if strings.TrimSpace(oldDef) != strings.TrimSpace(colDef) {
				stmts = append(stmts,
					fmt.Sprintf("ALTER TABLE `%s` MODIFY COLUMN %s;\n", tableName, colDef))
			}
		}
	}

	// Find INSERT statements present in new but not in old
	oldInserts := make(map[string]bool)
	for _, ins := range extractInserts(oldSQL) {
		oldInserts[ins] = true
	}
	for _, ins := range extractInserts(newSQL) {
		if !oldInserts[ins] {
			stmts = append(stmts, ins+"\n")
		}
	}

	return strings.Join(stmts, "\n")
}
