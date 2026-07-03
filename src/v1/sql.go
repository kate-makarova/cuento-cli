package main

import (
	"fmt"
	"regexp"
	"sort"
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

var reFKRef = regexp.MustCompile(`(?i)REFERENCES\s+` + "`?(\\w+)`?")

// fkDeps returns the set of table names that tableDDL references via FOREIGN KEY.
func fkDeps(tableDDL string) map[string]bool {
	deps := map[string]bool{}
	for _, m := range reFKRef.FindAllStringSubmatch(tableDDL, -1) {
		deps[strings.ToLower(m[1])] = true
	}
	return deps
}

// topoSort returns newTableNames ordered so that every table appears after the
// tables it depends on (via FK). Tables with no ordering constraint are sorted
// alphabetically for determinism.
func topoSort(newTableNames []string, ddlOf map[string]string) []string {
	deps := make(map[string]map[string]bool, len(newTableNames))
	newSet := make(map[string]bool, len(newTableNames))
	for _, t := range newTableNames {
		newSet[t] = true
	}
	for _, t := range newTableNames {
		d := fkDeps(ddlOf[t])
		// Only count deps that are also new tables (existing tables are already there).
		filtered := map[string]bool{}
		for ref := range d {
			if newSet[ref] && ref != t {
				filtered[ref] = true
			}
		}
		deps[t] = filtered
	}

	var sorted []string
	visited := map[string]bool{}
	var visit func(t string)
	visit = func(t string) {
		if visited[t] {
			return
		}
		visited[t] = true
		refs := make([]string, 0, len(deps[t]))
		for r := range deps[t] {
			refs = append(refs, r)
		}
		sort.Strings(refs)
		for _, r := range refs {
			visit(r)
		}
		sorted = append(sorted, t)
	}
	alpha := make([]string, len(newTableNames))
	copy(alpha, newTableNames)
	sort.Strings(alpha)
	for _, t := range alpha {
		visit(t)
	}
	return sorted
}

// generateMigration compares two SQL schemas and returns the migration SQL needed
// to bring the old schema up to the new one (new tables, new columns, new inserts).
func generateMigration(oldSQL, newSQL string) string {
	oldTables := parseTables(oldSQL)
	newTables := parseTables(newSQL)

	// Collect DDL blocks for new tables and sort them by FK dependency order.
	newTableDDL := map[string]string{}
	for _, mm := range reCreateTable.FindAllStringSubmatch(newSQL, -1) {
		newTableDDL[strings.ToLower(mm[1])] = mm[0]
	}
	var newTableNames []string
	for tableName := range newTables {
		if _, exists := oldTables[tableName]; !exists {
			newTableNames = append(newTableNames, tableName)
		}
	}
	orderedNew := topoSort(newTableNames, newTableDDL)

	var stmts []string

	for _, tableName := range orderedNew {
		if ddl, ok := newTableDDL[tableName]; ok {
			stmts = append(stmts, ddl+";\n")
		}
	}

	for tableName, newCols := range newTables {
		oldCols, exists := oldTables[tableName]
		if !exists {
			continue
		}
		// Table exists — find new or changed columns
		for colName, colDef := range newCols {
			cleanDef := strings.Join(strings.Fields(colDef), " ")
			if oldDef, known := oldCols[colName]; !known {
				stmts = append(stmts,
					fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN %s;\n", tableName, cleanDef))
			} else if normalizeColDef(oldDef) != normalizeColDef(colDef) {
				stmts = append(stmts,
					fmt.Sprintf("ALTER TABLE `%s` MODIFY COLUMN %s;\n", tableName, cleanDef))
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
