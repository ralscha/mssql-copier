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
	server := strings.TrimSpace(u.Hostname())
	if instance := strings.Trim(strings.TrimSpace(u.Path), "/"); instance != "" {
		server += `\` + instance
	}
	form := sqlServerDSNForm{
		Server:                 server,
		Port:                   strings.TrimSpace(u.Port()),
		Database:               firstNonEmptyQueryValue(query, "database", "initial catalog"),
		Username:               firstNonEmptyQueryValue(query, "user id", "uid", "user"),
		Password:               firstNonEmptyQueryValue(query, "password", "pwd"),
		Encrypt:                firstNonEmptyQueryValue(query, "encrypt"),
		TrustServerCertificate: firstNonEmptyQueryValue(query, "trustservercertificate"),
		Options: encodeExtraQueryOptions(query, map[string]struct{}{
			"database":               {},
			"initial catalog":        {},
			"user id":                {},
			"uid":                    {},
			"user":                   {},
			"password":               {},
			"pwd":                    {},
			"encrypt":                {},
			"trustservercertificate": {},
		}),
	}
	if u.User != nil {
		form.Username = u.User.Username()
		if password, ok := u.User.Password(); ok {
			form.Password = password
		}
		if queryUsername := firstNonEmptyQueryValue(query, "user id", "uid", "user"); queryUsername != "" {
			form.Username = queryUsername
		}
		if queryPassword := firstNonEmptyQueryValue(query, "password", "pwd"); queryPassword != "" {
			form.Password = queryPassword
		}
	}
	return form, true
}

func parseSQLServerKeyValueDSNForm(dsn string) sqlServerDSNForm {
	form := sqlServerDSNForm{}
	if len(dsn) >= len("odbc:") && strings.EqualFold(dsn[:len("odbc:")], "odbc:") {
		dsn = dsn[len("odbc:"):]
	}
	extra := make([]string, 0)
	for _, part := range splitSQLServerDSNSegments(dsn) {
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
		value = decodeSQLServerDSNValue(strings.TrimSpace(value))
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
			encoded, _ := encodeSQLServerDSNValue(value)
			extra = append(extra, strings.TrimSpace(key)+"="+encoded)
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

	parts := make([]string, 0, 8)
	requiresODBC := false
	appendSetting := func(key, value string) {
		encoded, braced := encodeSQLServerDSNValue(value)
		requiresODBC = requiresODBC || braced
		parts = append(parts, key+"="+encoded)
	}
	appendSetting("server", server)
	if port != "" {
		appendSetting("port", port)
	}
	if database != "" {
		appendSetting("database", database)
	}
	if username != "" {
		appendSetting("user id", username)
	}
	if password != "" {
		appendSetting("password", password)
	}
	if encrypt != "" {
		appendSetting("encrypt", encrypt)
	}
	if trust != "" {
		appendSetting("trustservercertificate", trust)
	}

	if options != "" {
		for _, option := range splitSQLServerDSNSegments(options) {
			option = strings.TrimSpace(option)
			if option == "" {
				continue
			}
			key, value, ok := strings.Cut(option, "=")
			if !ok {
				return "", fmt.Errorf("option %q must use key=value", option)
			}
			appendSetting(strings.TrimSpace(key), decodeSQLServerDSNValue(strings.TrimSpace(value)))
		}
	}

	prefix := ""
	if requiresODBC {
		prefix = "odbc:"
	}
	return prefix + strings.Join(parts, ";"), nil
}

func splitSQLServerDSNSegments(dsn string) []string {
	segments := make([]string, 0, strings.Count(dsn, ";")+1)
	start := 0
	inBraces := false
	seenEquals := false
	valueStarted := false
	for index := 0; index < len(dsn); index++ {
		char := dsn[index]
		if inBraces {
			if char != '}' {
				continue
			}
			if index+1 < len(dsn) && dsn[index+1] == '}' {
				index++
				continue
			}
			inBraces = false
			continue
		}
		if char == ';' {
			segments = append(segments, dsn[start:index])
			start = index + 1
			seenEquals = false
			valueStarted = false
			continue
		}
		if !seenEquals {
			if char == '=' {
				seenEquals = true
			}
			continue
		}
		if valueStarted || char == ' ' || char == '\t' {
			continue
		}
		valueStarted = true
		inBraces = char == '{'
	}
	segments = append(segments, dsn[start:])
	return segments
}

func decodeSQLServerDSNValue(value string) string {
	if len(value) >= 2 && value[0] == '{' && value[len(value)-1] == '}' {
		return strings.ReplaceAll(value[1:len(value)-1], "}}", "}")
	}
	return value
}

func encodeSQLServerDSNValue(value string) (string, bool) {
	if !strings.ContainsAny(value, ";{}") {
		return value, false
	}
	return "{" + strings.ReplaceAll(value, "}", "}}") + "}", true
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
		for actualKey, entries := range values {
			if !strings.EqualFold(actualKey, key) {
				continue
			}
			for _, entry := range entries {
				if value := strings.TrimSpace(entry); value != "" {
					return value
				}
			}
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
