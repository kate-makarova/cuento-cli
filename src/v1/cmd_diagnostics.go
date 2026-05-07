package main

import (
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ─── DATABASE DIAGNOSTICS ─────────────────────────────────────────────────────

type riskLevel int

const (
	riskNone    riskLevel = 0
	riskMinimal riskLevel = 1
	riskLow     riskLevel = 2
	riskMedium  riskLevel = 3
	riskHigh    riskLevel = 4
)

func (r riskLevel) label() string {
	switch r {
	case riskMinimal:
		return "Minimal"
	case riskLow:
		return "Low"
	case riskMedium:
		return "Medium"
	case riskHigh:
		return "High"
	default:
		return "—"
	}
}

func (r riskLevel) colored() string {
	switch r {
	case riskMinimal:
		return green("Minimal")
	case riskLow:
		return cyan("Low")
	case riskMedium:
		return yellow("Medium")
	case riskHigh:
		return red("High")
	default:
		return "—"
	}
}

var (
	reNormWS = regexp.MustCompile(`\s+`)
	// MariaDB puts NOT NULL before DEFAULT; canonicalise to NOT NULL before DEFAULT.
	reDefaultBeforeNull = regexp.MustCompile(`(?i)\bdefault\s+(\S+)\s+not\s+null\b`)
	reCharsetSQL        = regexp.MustCompile(`(?i)\s+(character\s+set|charset|collate)\s+\S+`)
	// MariaDB 10.5+ drops display widths for integer types (int(11) → int).
	reIntWidth = regexp.MustCompile(`\b(tinyint|smallint|mediumint|int|integer|bigint|year)\(\d+\)`)
	// mysqldump moves inline PRIMARY KEY / UNIQUE [KEY] to separate constraint lines.
	reInlineKey = regexp.MustCompile(`\s+(primary\s+key|unique\s+key|unique)\b`)
	// NULL and DEFAULT NULL are semantically equivalent for nullable columns.
	// Match only when not preceded by NOT (which we protect with a placeholder).
	reNullClause = regexp.MustCompile(`\s+(default\s+)?null\b`)
	// MariaDB stores boolean/bool as tinyint(1).
	reBoolean = regexp.MustCompile(`\b(boolean|bool)\b`)
	// Matches decimal with or without precision — used to normalise bare decimal.
	reDecimal = regexp.MustCompile(`\bdecimal(\s*\([^)]*\))?`)
	// MariaDB stores json as longtext with a CHECK constraint.
	reJSON = regexp.MustCompile(`\bjson\b`)
	// Strip CHECK (...) including one level of nested parens: CHECK (json_valid(`col`))
	reCheckExpr = regexp.MustCompile(`\s+check\s*\((?:[^()]*|\([^()]*\))*\)`)
)

func normalizeColDef(def string) string {
	def = strings.ReplaceAll(def, "`", "")
	def = reCharsetSQL.ReplaceAllString(def, "")
	def = strings.ToLower(strings.TrimSpace(def))
	def = strings.ReplaceAll(def, "current_timestamp()", "current_timestamp")
	// MariaDB stores boolean literals as integers.
	def = strings.ReplaceAll(def, "default false", "default 0")
	def = strings.ReplaceAll(def, "default true", "default 1")
	// MariaDB stores boolean as tinyint(1) — must run before reIntWidth so the
	// introduced tinyint(1) gets its display width stripped in the same pass.
	def = reBoolean.ReplaceAllString(def, "tinyint(1)")
	// Strip deprecated display widths from integer/year types.
	def = reIntWidth.ReplaceAllString(def, "$1")
	// Plain decimal without precision defaults to decimal(10,0) in MariaDB.
	def = reDecimal.ReplaceAllStringFunc(def, func(s string) string {
		if strings.Contains(s, "(") {
			return s
		}
		return "decimal(10,0)"
	})
	// MariaDB stores json as longtext with a CHECK constraint; treat them as equal.
	def = reJSON.ReplaceAllString(def, "longtext")
	def = reCheckExpr.ReplaceAllString(def, "")
	// AUTO_INCREMENT columns are implicitly NOT NULL; make it explicit so both
	// sides of the comparison carry "not null".
	if strings.Contains(def, "auto_increment") && !strings.Contains(def, "not null") {
		def = strings.Replace(def, "auto_increment", "not null auto_increment", 1)
	}
	// Canonicalise: MariaDB always emits NOT NULL before AUTO_INCREMENT.
	def = strings.ReplaceAll(def, "auto_increment not null", "not null auto_increment")
	// Inline PRIMARY KEY implies NOT NULL — make it explicit before stripping the keyword.
	if strings.Contains(def, "primary key") && !strings.Contains(def, "not null") {
		def = strings.Replace(def, "primary key", "not null primary key", 1)
	}
	// mysqldump moves inline PRIMARY KEY / UNIQUE to a separate constraint line.
	def = reInlineKey.ReplaceAllString(def, "")
	// NULL and DEFAULT NULL are equivalent. Protect "not null" with a placeholder
	// before stripping bare null / default null so it is not accidentally removed.
	// Canonicalise clause order: MariaDB emits NOT NULL before DEFAULT, but SQL
	// files often write DEFAULT first. Normalise to "not null default <val>".
	def = reDefaultBeforeNull.ReplaceAllString(def, "not null default $1")
	// NULL and DEFAULT NULL are equivalent. Protect "not null" with a placeholder
	// before stripping bare null / default null so it is not accidentally removed.
	def = strings.ReplaceAll(def, "not null", "\x01")
	def = reNullClause.ReplaceAllString(def, "")
	def = strings.ReplaceAll(def, "\x01", "not null")
	def = reNormWS.ReplaceAllString(def, " ")
	def = strings.ReplaceAll(def, ", ", ",")
	return strings.TrimSpace(def)
}

var reExtractType = regexp.MustCompile(`(?i)^\w+\s+(\w+)(?:\((\d+))?`)

func extractBaseType(colDef string) (string, int) {
	def := strings.ReplaceAll(strings.TrimSpace(colDef), "`", "")
	m := reExtractType.FindStringSubmatch(def)
	if m == nil {
		return "", 0
	}
	t := strings.ToLower(m[1])
	size := 0
	if len(m) > 2 && m[2] != "" {
		size, _ = strconv.Atoi(m[2])
	}
	return t, size
}

func numericTypeRank(t string) int {
	switch t {
	case "tinyint":
		return 1
	case "smallint":
		return 2
	case "mediumint":
		return 3
	case "int", "integer":
		return 4
	case "bigint":
		return 5
	case "float":
		return 6
	case "double", "real":
		return 7
	}
	return 0
}

func textTypeRank(t string) int {
	switch t {
	case "tinytext":
		return 1
	case "text":
		return 2
	case "mediumtext":
		return 3
	case "longtext":
		return 4
	}
	return 0
}

func isNullableOrHasDefault(colDef string) bool {
	upper := strings.ToUpper(colDef)
	return !strings.Contains(upper, "NOT NULL") || strings.Contains(upper, "DEFAULT")
}

func assessTypeRisk(oldDef, newDef string) (riskLevel, string) {
	oldType, oldSize := extractBaseType(oldDef)
	newType, newSize := extractBaseType(newDef)

	if oldType == newType {
		if oldSize == 0 || newSize == 0 || newSize >= oldSize {
			return riskLow, "column type expanded"
		}
		return riskHigh, "column type shrunk"
	}

	oldNum := numericTypeRank(oldType)
	newNum := numericTypeRank(newType)
	if oldNum > 0 && newNum > 0 {
		if newNum >= oldNum {
			return riskLow, "numeric type expanded"
		}
		return riskHigh, "numeric type shrunk"
	}

	oldText := textTypeRank(oldType)
	newText := textTypeRank(newType)
	if oldText > 0 && newText > 0 {
		if newText >= oldText {
			return riskLow, "text type expanded"
		}
		return riskHigh, "text type shrunk"
	}

	return riskHigh, "data type changed"
}

type colChange struct {
	name   string
	oldDef string // empty = new column
	newDef string // empty = dropped column
	risk   riskLevel
	reason string
}

// fetchLiveSchema uses SHOW CREATE TABLE for each table so the returned
// CREATE statements match what MySQL actually stores, avoiding the formatting
// differences that mysqldump introduces.
func (r *Remote) fetchLiveSchema(dbUser, dbPass, dbName string) (string, error) {
	tablesOut, err := r.runWithOutput(
		fmt.Sprintf(`MYSQL_PWD='%s' mysql -u %s %s --skip-column-names -e "SHOW TABLES"`, dbPass, dbUser, dbName),
		"",
	)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for _, tableName := range strings.Split(strings.TrimSpace(tablesOut), "\n") {
		tableName = strings.TrimSpace(tableName)
		if tableName == "" {
			continue
		}
		createOut, err := r.runWithOutput(
			fmt.Sprintf("MYSQL_PWD='%s' mysql -u %s %s --skip-column-names -e 'SHOW CREATE TABLE %s'", dbPass, dbUser, dbName, tableName),
			"",
		)
		if err != nil {
			return "", fmt.Errorf("SHOW CREATE TABLE %s: %w", tableName, err)
		}
		// Output is two tab-separated columns: tablename\tCREATE TABLE ...
		// MySQL batch mode escapes newlines as \n and tabs as \t within values.
		parts := strings.SplitN(createOut, "\t", 2)
		if len(parts) == 2 {
			stmt := parts[1]
			stmt = strings.ReplaceAll(stmt, `\n`, "\n")
			stmt = strings.ReplaceAll(stmt, `\t`, "\t")
			stmt = strings.TrimSpace(stmt)
			if !strings.HasSuffix(stmt, ";") {
				stmt += ";"
			}
			sb.WriteString(stmt)
			sb.WriteString("\n\n")
		}
	}
	return sb.String(), nil
}

func (r *Remote) queryRowCount(dbUser, dbPass, dbName, tableName string) (int, error) {
	out, err := r.runWithOutput(
		fmt.Sprintf(`MYSQL_PWD='%s' mysql -u %s %s --skip-column-names`, dbPass, dbUser, dbName),
		fmt.Sprintf("SELECT COUNT(*) FROM `%s`;\n", tableName),
	)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("unexpected row count output: %q", strings.TrimSpace(out))
	}
	return n, nil
}

func extractCreateTableSQL(fullSQL, tableName string) string {
	for _, m := range reCreateTable.FindAllStringSubmatch(fullSQL, -1) {
		if strings.ToLower(m[1]) == strings.ToLower(tableName) {
			stmt := strings.TrimSpace(m[0])
			if !strings.HasSuffix(stmt, ";") {
				stmt += ";"
			}
			return stmt
		}
	}
	return ""
}

func runDiagnostics(app *AppConfig, projectName string, saved *ProjectConfig) {
	if saved == nil {
		fatalExit(red("  ✗ No saved config for project " + projectName))
	}

	cfg := &Config{}
	cfg.ProjectName = projectName
	cfg.GitHubToken = saved.GitHubToken
	cfg.GitHubUser = saved.GitHubUser
	cfg.BackendFork = saved.GitHubUser + "/" + projectName + "-backend"
	cfg.ServerIP = saved.ServerIP
	cfg.SSHUser = saved.SSHUser
	cfg.SSHPass = saved.SSHPass
	cfg.SudoPass = saved.SudoPass
	cfg.DBName = saved.DBName
	cfg.DBUser = saved.DBUser
	cfg.DBPass = saved.DBPass

	if cfg.GitHubToken != "" {
		cmd := exec.Command("gh", "auth", "login", "--with-token")
		cmd.Stdin = strings.NewReader(cfg.GitHubToken + "\n")
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		_ = cmd.Run()
	}

	// ── Gather data ──────────────────────────────────────────────────────────
	fmt.Print("\n▶  " + bold("Fetching schema from GitHub") + "\n")
	sqlSchema, err := ghReadFile(cfg.BackendFork, sqlFile, trackBranch)
	if err != nil {
		fatalExit(red("   ✗ " + err.Error()))
	}
	fmt.Println(green("   ✓ Done"))

	fmt.Print("\n▶  " + bold("Connecting to server") + "\n")
	client, err := connectSSH(cfg.ServerIP, "22", cfg.SSHUser, cfg.SSHPass)
	if err != nil {
		fatalExit(red("   ✗ " + err.Error()))
	}
	remote := &Remote{client: client, sudoPass: cfg.SudoPass}
	defer remote.client.Close()
	fmt.Println(green("   ✓ Done"))

	fmt.Print("\n▶  " + bold("Fetching live database schema") + "\n")
	liveSchema, err := remote.fetchLiveSchema(cfg.DBUser, cfg.DBPass, cfg.DBName)
	if err != nil {
		fatalExit(red("   ✗ " + err.Error()))
	}
	fmt.Println(green("   ✓ Done"))

	// ── Compare ──────────────────────────────────────────────────────────────
	sqlTables := parseTables(sqlSchema)
	liveTables := parseTables(liveSchema)

	sep := strings.Repeat("─", 52)

	tableNames := make([]string, 0, len(sqlTables))
	for t := range sqlTables {
		tableNames = append(tableNames, t)
	}
	sort.Strings(tableNames)

	fmt.Println()
	fmt.Println(bold(sep))
	fmt.Printf("  %s — %s\n", bold("Database Diagnostic Report"), cyan(cfg.DBName))
	fmt.Println(bold(sep))

	for _, tableName := range tableNames {
		sqlCols := sqlTables[tableName]
		fmt.Println()
		fmt.Printf("  Table: %s\n", bold(tableName))

		fmt.Printf("  [SQL file]\n%s\n\n", extractCreateTableSQL(sqlSchema, tableName))
		fmt.Printf("  [Live DB]\n%s\n\n", extractCreateTableSQL(liveSchema, tableName))

		liveCols, exists := liveTables[tableName]
		if !exists {
			fmt.Printf("  Status: %s\n", yellow("MISSING — not in database"))
			createSQL := extractCreateTableSQL(sqlSchema, tableName)
			if createSQL != "" && confirm("  Create this table?") {
				if applyErr := remote.runWithInput(
					fmt.Sprintf(`MYSQL_PWD='%s' mysql -u %s %s`, cfg.DBPass, cfg.DBUser, cfg.DBName),
					createSQL+"\n",
				); applyErr != nil {
					fmt.Println(red("   ✗ " + applyErr.Error()))
				} else {
					fmt.Println(green("   ✓ Table created."))
				}
			}
			fmt.Println(sep)
			continue
		}

		// Collect column differences
		var changes []colChange
		for colName, newDef := range sqlCols {
			oldDef, known := liveCols[colName]
			if !known {
				risk, reason := riskLow, "new nullable column"
				if !isNullableOrHasDefault(newDef) {
					risk, reason = riskMedium, "new NOT NULL column without default"
				}
				changes = append(changes, colChange{name: colName, newDef: newDef, risk: risk, reason: reason})
			} else if normalizeColDef(oldDef) != normalizeColDef(newDef) {
				risk, reason := assessTypeRisk(oldDef, newDef)
				changes = append(changes, colChange{name: colName, oldDef: oldDef, newDef: newDef, risk: risk, reason: reason})
			}
		}
		// _flattened tables have dynamic extra columns — only validate what the
		// SQL file defines, ignore any additional live columns.
		if !strings.HasSuffix(tableName, "_flattened") {
			for colName, oldDef := range liveCols {
				if _, inSQL := sqlCols[colName]; !inSQL {
					changes = append(changes, colChange{name: colName, oldDef: oldDef, risk: riskHigh, reason: "column to be removed"})
				}
			}
		}

		if len(changes) == 0 {
			fmt.Printf("  Status: %s\n", green("OK ✓"))
			fmt.Println(sep)
			continue
		}

		fmt.Printf("  Status: %s\n", yellow("DIFFERENCES FOUND"))

		rowCount, countErr := remote.queryRowCount(cfg.DBUser, cfg.DBPass, cfg.DBName, tableName)
		if countErr != nil {
			fmt.Printf("  %s\n", yellow("⚠  Could not get row count: "+countErr.Error()))
		} else {
			fmt.Printf("  Rows: %d\n", rowCount)
		}

		sort.Slice(changes, func(i, j int) bool { return changes[i].name < changes[j].name })

		overallRisk := riskNone
		for _, c := range changes {
			if c.risk > overallRisk {
				overallRisk = c.risk
			}
		}

		fmt.Println()
		fmt.Println("  Changes:")
		for _, c := range changes {
			var marker, def string
			switch {
			case c.oldDef == "":
				marker = green("+")
				def = c.newDef
			case c.newDef == "":
				marker = red("-")
				def = c.oldDef
			default:
				marker = yellow("~")
				def = c.newDef
			}
			fmt.Printf("    %s  %s\n       Risk: %s — %s\n", marker, def, c.risk.colored(), c.reason)
		}

		// Generate ALTER TABLE statements
		var alters []string
		for _, c := range changes {
			switch {
			case c.oldDef == "":
				alters = append(alters, fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN %s;", tableName, c.newDef))
			case c.newDef == "":
				alters = append(alters, fmt.Sprintf("ALTER TABLE `%s` DROP COLUMN `%s`;", tableName, c.name))
			default:
				alters = append(alters, fmt.Sprintf("ALTER TABLE `%s` MODIFY COLUMN %s;", tableName, c.newDef))
			}
		}

		fmt.Println()
		if countErr == nil && rowCount == 0 {
			fmt.Printf("  Risk level: %s (table is empty — safe to recreate)\n", green("Minimal"))
			createSQL := extractCreateTableSQL(sqlSchema, tableName)
			if createSQL != "" && confirm("  Recreate this table from schema?") {
				recreateSQL := fmt.Sprintf("DROP TABLE IF EXISTS `%s`;\n%s\n", tableName, createSQL)
				if applyErr := remote.runWithInput(
					fmt.Sprintf(`MYSQL_PWD='%s' mysql -u %s %s`, cfg.DBPass, cfg.DBUser, cfg.DBName),
					recreateSQL,
				); applyErr != nil {
					fmt.Println(red("   ✗ " + applyErr.Error()))
				} else {
					fmt.Println(green("   ✓ Table recreated."))
				}
			}
		} else {
			fmt.Printf("  Overall risk: %s\n", overallRisk.colored())
			fmt.Println()
			fmt.Println("  Generated SQL:")
			for _, a := range alters {
				fmt.Println("    " + a)
			}
			fmt.Println()
			if confirm("  Apply these changes?") {
				if applyErr := remote.runWithInput(
					fmt.Sprintf(`MYSQL_PWD='%s' mysql -u %s %s`, cfg.DBPass, cfg.DBUser, cfg.DBName),
					strings.Join(alters, "\n")+"\n",
				); applyErr != nil {
					fmt.Println(red("   ✗ " + applyErr.Error()))
				} else {
					fmt.Println(green("   ✓ Changes applied."))
				}
			}
		}
		fmt.Println(sep)
	}

	// Report tables in live DB not in schema file
	for tableName := range liveTables {
		if _, inSQL := sqlTables[tableName]; !inSQL {
			fmt.Println()
			fmt.Printf("  Table: %s\n", bold(tableName))
			fmt.Printf("  Status: %s\n", yellow("EXTRA — not in schema file (no action taken)"))
			fmt.Println(sep)
		}
	}

	fmt.Println()
}
