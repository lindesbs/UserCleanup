package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MySQLAccount is a MySQL/MariaDB login found in a user's ~/.my.cnf, with
// the databases and tables it can see. The password is never exported.
type MySQLAccount struct {
	File     string `json:"file"`
	Owner    string `json:"owner"`
	Host     string `json:"host,omitempty"`
	Port     string `json:"port,omitempty"`
	Socket   string `json:"socket,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	User     string `json:"user"`
	// Database is the default database of the client (database=)
	Database string `json:"database,omitempty"`
	// Account is the account the server authenticated, e.g. web@localhost
	Account string `json:"account,omitempty"`
	Server  string `json:"server_version,omitempty"`
	// Global means the account has privileges on all databases (like root);
	// its databases then do not belong to the user
	Global    bool             `json:"global_privileges,omitempty"`
	Databases []*MySQLDatabase `json:"databases"`
	Error     string           `json:"error,omitempty"`
	password  string
}

// MySQLDatabase is a database an account can see
type MySQLDatabase struct {
	Name   string        `json:"name"`
	Tables []*MySQLTable `json:"tables"`
	// SharedWith are other users whose ~/.my.cnf reaches the same database
	SharedWith []string `json:"shared_with,omitempty"`
}

// MySQLTable is one table or view; Rows is an estimate for InnoDB
type MySQLTable struct {
	Name   string `json:"name"`
	Engine string `json:"engine"`
	Rows   int64  `json:"rows"`
	Size   int64  `json:"size"`
}

// Rows returns the estimated row count of all tables
func (db *MySQLDatabase) Rows() int64 {
	var rows int64
	for _, table := range db.Tables {
		rows += table.Rows
	}
	return rows
}

// Size returns the data and index size of all tables in bytes
func (db *MySQLDatabase) Size() int64 {
	var size int64
	for _, table := range db.Tables {
		size += table.Size
	}
	return size
}

// mysqlSystemSchemas are never collected, dumped or dropped
var mysqlSystemSchemas = []string{"information_schema", "mysql", "performance_schema", "sys"}

// mysqlSystemAccounts are never dropped by generated commands
var mysqlSystemAccounts = map[string]bool{
	"root": true, "mysql": true, "mariadb.sys": true, "mysql.sys": true,
	"mysql.session": true, "mysql.infoschema": true, "debian-sys-maint": true,
}

// mysqlGroups are the option groups of ~/.my.cnf the mysql client reads
var mysqlGroups = map[string]bool{"client": true, "mysql": true, "client-server": true, "client-mariadb": true, "mariadb-client": true}

// Settings for the MySQL collection; variables so tests and the list
// command can change them
var (
	skipMySQL    bool
	mysqlTimeout = 30 * time.Second
	// runMySQLTool runs a MySQL client tool ("mysql" or "mysqldump") with a
	// defaults file and writes its output to stdout
	runMySQLTool = execMySQLTool
)

// Endpoint names the server of the account: the socket for local
// connections, otherwise host:port
func (a *MySQLAccount) Endpoint() string {
	if a.Host == "" || a.Host == "localhost" {
		if a.Protocol == "" || strings.EqualFold(a.Protocol, "socket") {
			if a.Socket != "" {
				return "localhost:" + filepath.Clean(a.Socket)
			}
			return "localhost"
		}
	}
	host := a.Host
	if host == "" {
		host = "localhost"
	}
	port := a.Port
	if port == "" {
		port = "3306"
	}
	return host + ":" + port
}

// Label describes the account for messages
func (a *MySQLAccount) Label() string {
	if a.Account != "" {
		return a.Account + " on " + a.Endpoint()
	}
	return a.User + " on " + a.Endpoint()
}

// parseMyCnf reads the connection options of an option file. Later values
// override earlier ones, like in the mysql client; "!include" lines are
// not followed.
func parseMyCnf(path string) (*MySQLAccount, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	acct := &MySQLAccount{File: path}
	group := ""
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' || line[0] == ';' || line[0] == '!' {
			continue
		}
		if strings.HasPrefix(line, "[") {
			group = strings.ToLower(strings.TrimSpace(strings.Trim(line, "[]")))
			continue
		}
		if !mysqlGroups[group] {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(key)), "_", "-")
		value = optionValue(value)
		switch key {
		case "host":
			acct.Host = value
		case "port":
			acct.Port = value
		case "socket":
			acct.Socket = value
		case "protocol":
			acct.Protocol = value
		case "user":
			acct.User = value
		case "password":
			acct.password = value
		case "database":
			acct.Database = value
		}
	}
	return acct, scanner.Err()
}

// optionValue unquotes an option file value and resolves its escapes;
// unquoted values end at a "#" comment
func optionValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') {
		if end := strings.LastIndexByte(value, value[0]); end > 0 {
			value = value[1:end]
		}
	} else if i := strings.IndexByte(value, '#'); i >= 0 {
		value = strings.TrimSpace(value[:i])
	}
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' || i == len(value)-1 {
			b.WriteByte(value[i])
			continue
		}
		i++
		switch value[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'b':
			b.WriteByte('\b')
		case 's':
			b.WriteByte(' ')
		case '\\', '"', '\'':
			b.WriteByte(value[i])
		default:
			b.WriteByte('\\')
			b.WriteByte(value[i])
		}
	}
	return b.String()
}

// quoteOption writes a value for an option file
func quoteOption(value string) string {
	r := strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + r.Replace(value) + `"`
}

// defaultsFile writes a temporary option file with only the connection
// options of the account. The user's own file is never passed to the
// client: run as root it could load client plugins from the user's home
// (plugin-dir) or allow the server to read local files (local-infile).
func (a *MySQLAccount) defaultsFile() (string, error) {
	f, err := os.CreateTemp("", "usercleanup-mysql-*.cnf")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("[client]\n")
	for _, opt := range [][2]string{
		{"host", a.Host}, {"port", a.Port}, {"socket", a.Socket},
		{"protocol", a.Protocol}, {"user", a.User}, {"password", a.password},
	} {
		if opt[1] != "" {
			fmt.Fprintf(&b, "%s=%s\n", opt[0], quoteOption(opt[1]))
		}
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// mysqlBinary finds a client tool; MariaDB also names them mariadb and
// mariadb-dump
func mysqlBinary(tool string) (string, error) {
	names := []string{tool, "mariadb"}
	if tool == "mysqldump" {
		names = []string{tool, "mariadb-dump"}
	}
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s client is not installed", tool)
}

func execMySQLTool(ctx context.Context, tool, defaults string, args []string, stdout io.Writer) error {
	bin, err := mysqlBinary(tool)
	if err != nil {
		return err
	}
	// --defaults-file must come first; no other option file is read
	cmd := exec.CommandContext(ctx, bin, append([]string{"--defaults-file=" + defaults}, args...)...)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// runTool runs a client tool for the account
func (a *MySQLAccount) runTool(ctx context.Context, tool string, args []string, stdout io.Writer) error {
	defaults, err := a.defaultsFile()
	if err != nil {
		return err
	}
	defer os.Remove(defaults)
	return runMySQLTool(ctx, tool, defaults, args, stdout)
}

// mysqlInventoryQuery returns one tagged row per fact, so a single client
// call returns everything
func mysqlInventoryQuery() string {
	skip := "('" + strings.Join(mysqlSystemSchemas, "','") + "')"
	return strings.Join([]string{
		"SELECT 'account', CURRENT_USER(), VERSION()",
		"SELECT 'privilege', PRIVILEGE_TYPE FROM information_schema.USER_PRIVILEGES WHERE GRANTEE = " +
			"CONCAT('''', SUBSTRING_INDEX(CURRENT_USER(), '@', 1), '''@''', SUBSTRING_INDEX(CURRENT_USER(), '@', -1), '''')",
		"SELECT 'schema', SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME NOT IN " + skip,
		"SELECT 'table', TABLE_SCHEMA, TABLE_NAME, IFNULL(ENGINE, TABLE_TYPE), IFNULL(TABLE_ROWS, 0), " +
			"IFNULL(DATA_LENGTH, 0) + IFNULL(INDEX_LENGTH, 0) FROM information_schema.TABLES WHERE TABLE_SCHEMA NOT IN " + skip,
	}, "; ") + ";"
}

// globalPrivileges give an account access to every database
var globalPrivileges = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true, "CREATE": true,
	"DROP": true, "ALTER": true, "INDEX": true, "SHOW DATABASES": true,
	"CREATE USER": true, "SUPER": true,
}

// unescapeBatch resolves the escapes of the client's batch output
func unescapeBatch(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	r := strings.NewReplacer(`\\`, `\`, `\t`, "\t", `\n`, "\n", `\0`, "\x00")
	return r.Replace(s)
}

// parseInventoryOutput fills the account from the batch output of
// mysqlInventoryQuery
func (a *MySQLAccount) parseInventoryOutput(out string) {
	dbs := make(map[string]*MySQLDatabase)
	db := func(name string) *MySQLDatabase {
		if dbs[name] == nil {
			dbs[name] = &MySQLDatabase{Name: name, Tables: []*MySQLTable{}}
		}
		return dbs[name]
	}
	for _, line := range strings.Split(out, "\n") {
		cols := strings.Split(line, "\t")
		for i := range cols {
			cols[i] = unescapeBatch(cols[i])
		}
		switch {
		case cols[0] == "account" && len(cols) >= 3:
			a.Account, a.Server = cols[1], cols[2]
		case cols[0] == "privilege" && len(cols) >= 2:
			if globalPrivileges[strings.ToUpper(cols[1])] {
				a.Global = true
			}
		case cols[0] == "schema" && len(cols) >= 2:
			db(cols[1])
		case cols[0] == "table" && len(cols) >= 6:
			rows, _ := strconv.ParseInt(cols[4], 10, 64)
			size, _ := strconv.ParseInt(cols[5], 10, 64)
			d := db(cols[1])
			d.Tables = append(d.Tables, &MySQLTable{Name: cols[2], Engine: cols[3], Rows: rows, Size: size})
		}
	}
	a.Databases = []*MySQLDatabase{}
	for _, d := range dbs {
		sort.Slice(d.Tables, func(i, j int) bool { return d.Tables[i].Name < d.Tables[j].Name })
		a.Databases = append(a.Databases, d)
	}
	sort.Slice(a.Databases, func(i, j int) bool { return a.Databases[i].Name < a.Databases[j].Name })
}

// collectMySQL reads ~/.my.cnf of every deletable user and asks the server
// which databases and tables the account can see. An unreachable server or
// a missing client is recorded in the account, never fatal.
func collectMySQL(all map[string]*User) []*MySQLAccount {
	if skipMySQL {
		return nil
	}
	var accounts []*MySQLAccount
	for _, user := range sortedUsers(all) {
		if isProtected(user) != "" || filepath.Clean(user.HomeDir) == "/" {
			continue
		}
		path := filepath.Join(user.HomeDir, ".my.cnf")
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		acct, err := parseMyCnf(path)
		if err != nil {
			accounts = append(accounts, &MySQLAccount{File: path, Owner: user.Username, User: user.Username,
				Databases: []*MySQLDatabase{}, Error: err.Error()})
			continue
		}
		acct.Owner = user.Username
		if acct.User == "" {
			// The client would use the login name, which is the user's
			acct.User = user.Username
		}
		ctx, cancel := context.WithTimeout(context.Background(), mysqlTimeout)
		var out bytes.Buffer
		err = acct.runTool(ctx, "mysql", []string{"--batch", "--skip-column-names", "--connect-timeout=5",
			"--local-infile=0", "-e", mysqlInventoryQuery()}, &out)
		cancel()
		if err != nil {
			acct.Databases = []*MySQLDatabase{}
			acct.Error = err.Error()
		} else {
			acct.parseInventoryOutput(out.String())
		}
		accounts = append(accounts, acct)
	}
	markSharedDatabases(accounts)
	return accounts
}

// markSharedDatabases notes databases on the same server that the
// accounts of several users can see. Accounts with global privileges see
// everything and are left out.
func markSharedDatabases(accounts []*MySQLAccount) {
	seen := make(map[string][]string)
	for _, acct := range accounts {
		if acct.Global {
			continue
		}
		for _, db := range acct.Databases {
			key := acct.Endpoint() + "/" + db.Name
			seen[key] = appendUnique(seen[key], acct.Owner)
		}
	}
	for _, acct := range accounts {
		if acct.Global {
			continue
		}
		for _, db := range acct.Databases {
			db.SharedWith = nil
			for _, owner := range seen[acct.Endpoint()+"/"+db.Name] {
				if owner != acct.Owner {
					db.SharedWith = append(db.SharedWith, owner)
				}
			}
		}
	}
}

// accountShared reports whether another user's ~/.my.cnf logs in with the
// same MySQL account
func accountShared(acct *MySQLAccount, accounts []*MySQLAccount) []string {
	var others []string
	for _, other := range accounts {
		if other.Owner != acct.Owner && other.Endpoint() == acct.Endpoint() && other.User == acct.User {
			others = appendUnique(others, other.Owner)
		}
	}
	return others
}

// dumpDatabases returns the databases of an account that belong into the
// backup: not for accounts that failed or see every database
func (a *MySQLAccount) dumpDatabases() []string {
	if a.Error != "" || a.Global {
		return nil
	}
	var names []string
	for _, db := range a.Databases {
		names = append(names, db.Name)
	}
	return names
}

// dump writes a mysqldump of the databases to w
func (a *MySQLAccount) dump(databases []string, w io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	args := []string{"--single-transaction", "--routines", "--triggers", "--no-tablespaces", "--databases", "--"}
	return a.runTool(ctx, "mysqldump", append(args, databases...), w)
}

// sqlIdent quotes a MySQL identifier
func sqlIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// sqlString quotes a MySQL string literal
func sqlString(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", "''") + "'"
}

// sqlAccount quotes user@host as 'user'@'host'
func sqlAccount(account string) string {
	i := strings.LastIndexByte(account, '@')
	if i < 0 {
		return sqlString(account) + "@'%'"
	}
	return sqlString(account[:i]) + "@" + sqlString(account[i+1:])
}

// adminCommand returns the mysql call for the administrator to run a
// statement on the account's server. It uses root's own credentials (unix
// socket or /root/.my.cnf), never the user's file, which is removed with
// the home.
func (a *MySQLAccount) adminCommand(statement string) string {
	args := []string{"mysql"}
	if a.Host != "" && a.Host != "localhost" {
		args = append(args, "-h", shellQuote(a.Host))
		if a.Port != "" {
			args = append(args, "-P", shellQuote(a.Port))
		}
	} else if a.Socket != "" {
		args = append(args, "-S", shellQuote(a.Socket))
	}
	return strings.Join(append(args, "-e", shellQuote(statement)), " ")
}

// formatBytes prints a size like 1.5 MiB
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
