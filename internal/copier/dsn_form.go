package copier

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type sqlServerDSNForm struct {
	Server                 string
	Port                   string
	Database               string
	Username               string
	Password               string
	Encrypt                string
	TrustServerCertificate string
	Options                string
}

func parseSQLServerDSNForm(dsn string) sqlServerDSNForm {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return sqlServerDSNForm{}
	}
	if parsed, ok := parseSQLServerURLDSNForm(dsn); ok {
		return parsed
	}
	return parseSQLServerKeyValueDSNForm(dsn)
}

func parseSQLServerURLDSNForm(dsn string) (sqlServerDSNForm, bool) {
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return sqlServerDSNForm{}, false
	}

	query := u.Query()
	form := sqlServerDSNForm{
		Server:                 strings.TrimSpace(u.Hostname()),
		Port:                   strings.TrimSpace(u.Port()),
		Database:               firstNonEmptyQueryValue(query, "database", "initial catalog"),
		Encrypt:                firstNonEmptyQueryValue(query, "encrypt"),
		TrustServerCertificate: firstNonEmptyQueryValue(query, "trustservercertificate"),
		Options: encodeExtraQueryOptions(query, map[string]struct{}{
			"database":               {},
			"initial catalog":        {},
			"encrypt":                {},
			"trustservercertificate": {},
		}),
	}
	if u.User != nil {
		form.Username = u.User.Username()
		if password, ok := u.User.Password(); ok {
			form.Password = password
		}
	}
	return form, true
}

func parseSQLServerKeyValueDSNForm(dsn string) sqlServerDSNForm {
	form := sqlServerDSNForm{}
	extra := make([]string, 0)
	for part := range strings.SplitSeq(dsn, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			extra = append(extra, part)
			continue
		}
		key = strings.TrimSpace(strings.ToLower(key))
		value = strings.TrimSpace(value)
		switch key {
		case "server", "data source", "addr", "address", "network address":
			form.Server, form.Port = splitSQLServerServerValue(value, form.Port)
		case "port":
			form.Port = value
		case "database", "initial catalog":
			form.Database = value
		case "user id", "uid", "user":
			form.Username = value
		case "password", "pwd":
			form.Password = value
		case "encrypt":
			form.Encrypt = value
		case "trustservercertificate":
			form.TrustServerCertificate = value
		default:
			extra = append(extra, part)
		}
	}
	form.Options = strings.Join(extra, ";")
	return form
}

func buildSQLServerDSN(form sqlServerDSNForm) (string, error) {
	server := strings.TrimSpace(form.Server)
	port := strings.TrimSpace(form.Port)
	database := strings.TrimSpace(form.Database)
	username := strings.TrimSpace(form.Username)
	password := strings.TrimSpace(form.Password)
	encrypt := strings.TrimSpace(form.Encrypt)
	trust := strings.TrimSpace(form.TrustServerCertificate)
	options := strings.TrimSpace(form.Options)

	if server == "" && port == "" && database == "" && username == "" && password == "" && encrypt == "" && trust == "" && options == "" {
		return "", nil
	}
	if server == "" {
		return "", fmt.Errorf("server is required")
	}

	parts := []string{"server=" + server}
	if port != "" {
		parts = append(parts, "port="+port)
	}
	if database != "" {
		parts = append(parts, "database="+database)
	}
	if username != "" {
		parts = append(parts, "user id="+username)
	}
	if password != "" {
		parts = append(parts, "password="+password)
	}
	if encrypt != "" {
		parts = append(parts, "encrypt="+encrypt)
	}
	if trust != "" {
		parts = append(parts, "trustservercertificate="+trust)
	}

	if options != "" {
		for option := range strings.SplitSeq(options, ";") {
			option = strings.TrimSpace(option)
			if option == "" {
				continue
			}
			if _, _, ok := strings.Cut(option, "="); !ok {
				return "", fmt.Errorf("option %q must use key=value", option)
			}
			parts = append(parts, option)
		}
	}

	return strings.Join(parts, ";"), nil
}

func sqlServerDSNDatabaseName(dsn string) string {
	return strings.TrimSpace(parseSQLServerDSNForm(dsn).Database)
}

func sqlServerDSNWithDatabase(dsn string, database string) (string, error) {
	form := parseSQLServerDSNForm(dsn)
	form.Database = strings.TrimSpace(database)
	return buildSQLServerDSN(form)
}

func firstNonEmptyQueryValue(values url.Values, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(values.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func encodeExtraQueryOptions(values url.Values, ignore map[string]struct{}) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if _, skipped := ignore[strings.ToLower(key)]; skipped {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		for _, value := range values[key] {
			parts = append(parts, strings.TrimSpace(key)+"="+strings.TrimSpace(value))
		}
	}
	return strings.Join(parts, ";")
}

func splitSQLServerServerValue(value string, existingPort string) (string, string) {
	value = strings.TrimSpace(strings.TrimPrefix(value, "tcp:"))
	if value == "" {
		return "", existingPort
	}
	if strings.HasPrefix(value, "[") {
		if end := strings.Index(value, "]"); end > 0 {
			host := strings.TrimSpace(value[:end+1])
			rest := strings.TrimSpace(value[end+1:])
			rest = strings.TrimPrefix(rest, ",")
			rest = strings.TrimPrefix(rest, ":")
			if rest != "" {
				return host, rest
			}
		}
	}
	if strings.Count(value, ",") == 1 && !strings.Contains(value, "\\") {
		if host, port, ok := strings.Cut(value, ","); ok {
			return strings.TrimSpace(host), strings.TrimSpace(port)
		}
	}
	if strings.Count(value, ":") == 1 && !strings.Contains(value, "\\") {
		if host, port, ok := strings.Cut(value, ":"); ok {
			return strings.TrimSpace(host), strings.TrimSpace(port)
		}
	}
	return value, existingPort
}
