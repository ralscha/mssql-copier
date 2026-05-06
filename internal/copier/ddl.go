package copier

import (
	"fmt"
	"regexp"
	"strings"
)

var viewHeaderPattern = regexp.MustCompile(`(?is)^\s*(?:create|alter)\s+view\s+(?:\[[^\]]+\]|[^\s.]+)\s*\.\s*(?:\[[^\]]+\]|[^\s]+)\s*`)
var functionHeaderPattern = regexp.MustCompile(`(?is)^\s*(?:create|alter)\s+function\s+(?:\[[^\]]+\]|[^\s.]+)\s*\.\s*(?:\[[^\]]+\]|[^\s(]+)\s*`)
var procedureHeaderPattern = regexp.MustCompile(`(?is)^\s*(?:create|alter)\s+(?:proc|procedure)\s+(?:\[[^\]]+\]|[^\s.]+)\s*\.\s*(?:\[[^\]]+\]|[^\s(]+)\s*`)
var triggerHeaderPattern = regexp.MustCompile(`(?is)^\s*(?:create|alter)\s+trigger\s+(?:\[[^\]]+\]|[^\s.]+)\s*\.\s*(?:\[[^\]]+\]|[^\s]+)\s*`)
var moduleBatchPreamblePattern = regexp.MustCompile(`(?im)^\s*(?:set\s+(?:ansi_nulls|quoted_identifier)\s+(?:on|off)\s*;?|go\s*;?)\s*$`)
var viewHeaderSearchPattern = regexp.MustCompile(`(?is)(?:create|alter)\s+view\s+(?:\[[^\]]+\]|[^\s.]+)\s*\.\s*(?:\[[^\]]+\]|[^\s]+)\s*`)
var functionHeaderSearchPattern = regexp.MustCompile(`(?is)(?:create|alter)\s+function\s+(?:\[[^\]]+\]|[^\s.]+)\s*\.\s*(?:\[[^\]]+\]|[^\s(]+)\s*`)
var procedureHeaderSearchPattern = regexp.MustCompile(`(?is)(?:create|alter)\s+(?:proc|procedure)\s+(?:\[[^\]]+\]|[^\s.]+)\s*\.\s*(?:\[[^\]]+\]|[^\s(]+)\s*`)
var triggerHeaderSearchPattern = regexp.MustCompile(`(?is)(?:create|alter)\s+trigger\s+(?:\[[^\]]+\]|[^\s.]+)\s*\.\s*(?:\[[^\]]+\]|[^\s]+)\s*`)

func (t tableMeta) FQTN() string {
	return quoteIdent(t.Schema) + "." + quoteIdent(t.Name)
}

func (t tableMeta) CreateTableSQL() (string, error) {
	if len(t.Columns) == 0 {
		return "", fmt.Errorf("table %s has no columns", t.FQTN())
	}
	parts := make([]string, 0, len(t.Columns))
	for _, col := range t.Columns {
		def, err := col.DefinitionSQL()
		if err != nil {
			return "", fmt.Errorf("%s.%s: %w", t.FQTN(), col.Name, err)
		}
		parts = append(parts, "    "+def)
	}
	return fmt.Sprintf("CREATE TABLE %s (\n%s\n);", t.FQTN(), strings.Join(parts, ",\n")), nil
}

func (t tableMeta) PrimaryKeySQL() string {
	cluster := strings.ToUpper(strings.TrimSpace(t.PrimaryKey.Cluster))
	if cluster == "" {
		cluster = "CLUSTERED"
	}
	return fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s PRIMARY KEY %s (%s);", t.FQTN(), quoteIdent(t.PrimaryKey.Name), cluster, joinKeyColumns(t.PrimaryKey.Columns))
}

func (t tableMeta) CheckSQL(check checkConstraint) string {
	mode := "WITH CHECK"
	if !check.Trusted {
		mode = "WITH NOCHECK"
	}
	return fmt.Sprintf("ALTER TABLE %s %s ADD CONSTRAINT %s CHECK %s;", t.FQTN(), mode, quoteIdent(check.Name), check.Definition)
}

func (t tableMeta) ForeignKeySQL(fk foreignKey) string {
	mode := "WITH CHECK"
	if !fk.Trusted {
		mode = "WITH NOCHECK"
	}
	var tail []string
	if action := normalizeReferentialAction(fk.DeleteAction); action != "" {
		tail = append(tail, "ON DELETE "+action)
	}
	if action := normalizeReferentialAction(fk.UpdateAction); action != "" {
		tail = append(tail, "ON UPDATE "+action)
	}
	stmt := fmt.Sprintf(
		"ALTER TABLE %s %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s.%s (%s)",
		t.FQTN(),
		mode,
		quoteIdent(fk.Name),
		joinQuotedNames(fk.Columns),
		quoteIdent(fk.RefSchema),
		quoteIdent(fk.RefTable),
		joinQuotedNames(fk.RefColumns),
	)
	if len(tail) > 0 {
		stmt += " " + strings.Join(tail, " ")
	}
	return stmt + ";"
}

func (t tableMeta) IndexSQL(index indexMeta) string {
	parts := []string{"CREATE"}
	if index.Unique {
		parts = append(parts, "UNIQUE")
	}
	cluster := strings.ToUpper(strings.TrimSpace(index.Cluster))
	if cluster == "" {
		cluster = "NONCLUSTERED"
	}
	parts = append(parts, cluster, "INDEX", quoteIdent(index.Name), "ON", t.FQTN(), "("+joinKeyColumns(index.KeyColumns)+")")
	stmt := strings.Join(parts, " ")
	if len(index.Include) > 0 {
		stmt += " INCLUDE (" + joinQuotedNames(index.Include) + ")"
	}
	if index.Filter != "" {
		stmt += " WHERE " + index.Filter
	}
	return stmt + ";"
}

func (c columnMeta) DefinitionSQL() (string, error) {
	if c.Computed {
		stmt := quoteIdent(c.Name) + " AS " + c.ComputedDefinition
		if c.ComputedPersisted {
			stmt += " PERSISTED"
		}
		return stmt, nil
	}

	typeDecl, err := c.TypeDeclaration()
	if err != nil {
		return "", err
	}
	parts := []string{quoteIdent(c.Name), typeDecl}
	if !c.IsUserDefined && c.Collation != "" && collationAllowed(c.SystemTypeName) {
		parts = append(parts, "COLLATE "+c.Collation)
	}
	if c.Sparse {
		parts = append(parts, "SPARSE")
	}
	if c.RowGuidCol {
		parts = append(parts, "ROWGUIDCOL")
	}
	if c.Identity {
		seed := c.IdentitySeed
		if seed == "" {
			seed = "1"
		}
		increment := c.IdentityIncrement
		if increment == "" {
			increment = "1"
		}
		parts = append(parts, fmt.Sprintf("IDENTITY(%s,%s)", seed, increment))
	}
	if c.Nullable {
		parts = append(parts, "NULL")
	} else {
		parts = append(parts, "NOT NULL")
	}
	if c.DefaultDefinition != "" {
		parts = append(parts, "DEFAULT "+c.DefaultDefinition)
	}
	return strings.Join(parts, " "), nil
}

func (c columnMeta) TypeDeclaration() (string, error) {
	if c.IsUserDefined {
		return quoteIdent(c.TypeSchema) + "." + quoteIdent(c.UserTypeName), nil
	}

	typeName := c.SystemTypeName
	switch typeName {
	case "varchar", "char", "varbinary", "binary":
		return typeName + lengthSuffix(c.MaxLength), nil
	case "nvarchar", "nchar":
		if c.MaxLength == -1 {
			return typeName + "(max)", nil
		}
		return fmt.Sprintf("%s(%d)", typeName, c.MaxLength/2), nil
	case "decimal", "numeric":
		return fmt.Sprintf("%s(%d,%d)", typeName, c.Precision, c.Scale), nil
	case "datetime2", "datetimeoffset", "time":
		return fmt.Sprintf("%s(%d)", typeName, c.Scale), nil
	case "float":
		if c.Precision > 0 && c.Precision != 53 {
			return fmt.Sprintf("float(%d)", c.Precision), nil
		}
		return "float", nil
	case "timestamp", "rowversion":
		return "rowversion", nil
	case "xml", "bigint", "bit", "date", "datetime", "geography", "geometry", "hierarchyid", "image", "int", "money", "ntext", "real", "smalldatetime", "smallint", "smallmoney", "text", "tinyint", "uniqueidentifier":
		return typeName, nil
	default:
		return "", fmt.Errorf("unsupported column type %s", typeName)
	}
}

func (a aliasTypeMeta) CreateTypeSQL() (string, error) {
	baseDecl, err := columnMeta{
		SystemTypeName: a.SystemTypeName,
		MaxLength:      a.MaxLength,
		Precision:      a.Precision,
		Scale:          a.Scale,
	}.TypeDeclaration()
	if err != nil {
		return "", fmt.Errorf("%s: %w", a.FQTN(), err)
	}

	nullability := "NOT NULL"
	if a.Nullable {
		nullability = "NULL"
	}

	createSQL := fmt.Sprintf("CREATE TYPE %s FROM %s %s;", a.FQTN(), baseDecl, nullability)
	if escapeSQLString(a.FQTN()) == "" {
		return createSQL, nil
	}
	return fmt.Sprintf("IF TYPE_ID(N'%s') IS NULL\nBEGIN\n    %s\nEND", escapeSQLString(a.FQTN()), createSQL), nil
}

func (t tableTypeMeta) CreateTypeSQL() (string, error) {
	if len(t.Columns) == 0 {
		return "", fmt.Errorf("table type %s has no columns", t.FQTN())
	}

	parts := make([]string, 0, len(t.Columns)+len(t.Checks)+1)
	for _, col := range t.Columns {
		def, err := col.DefinitionSQL()
		if err != nil {
			return "", fmt.Errorf("%s.%s: %w", t.FQTN(), col.Name, err)
		}
		parts = append(parts, "    "+def)
	}
	if t.PrimaryKey != nil {
		parts = append(parts, "    "+t.primaryKeyConstraintSQL())
	}
	for _, check := range t.Checks {
		parts = append(parts, "    "+t.checkConstraintSQL(check))
	}

	createSQL := fmt.Sprintf("CREATE TYPE %s AS TABLE (\n%s\n);", t.FQTN(), strings.Join(parts, ",\n"))
	return fmt.Sprintf("IF TYPE_ID(N'%s') IS NULL\nBEGIN\n    %s\nEND", escapeSQLString(t.FQTN()), createSQL), nil
}

func (t tableTypeMeta) primaryKeyConstraintSQL() string {
	cluster := strings.ToUpper(strings.TrimSpace(t.PrimaryKey.Cluster))
	if cluster == "" {
		cluster = "CLUSTERED"
	}
	return fmt.Sprintf("PRIMARY KEY %s (%s)", cluster, joinKeyColumns(t.PrimaryKey.Columns))
}

func (t tableTypeMeta) checkConstraintSQL(check checkConstraint) string {
	return fmt.Sprintf("CHECK %s", check.Definition)
}

func normalizeProgrammableDefinition(definition string, headerPattern *regexp.Regexp, headerSearchPattern *regexp.Regexp) string {
	definition = strings.TrimSpace(definition)
	definition = moduleBatchPreamblePattern.ReplaceAllString(definition, "")
	definition = strings.TrimSpace(definition)
	if match := headerSearchPattern.FindStringIndex(definition); match != nil {
		definition = definition[match[0]:]
	}
	definition = headerPattern.ReplaceAllString(definition, "")
	return strings.TrimSpace(definition)
}

func (v viewMeta) CreateViewSQL() string {
	definition := normalizeProgrammableDefinition(v.Definition, viewHeaderPattern, viewHeaderSearchPattern)
	return fmt.Sprintf("CREATE OR ALTER VIEW %s %s", v.FQTN(), definition)
}

func (s sequenceMeta) CreateSequenceSQL() (string, error) {
	typeDecl, err := columnMeta{SystemTypeName: s.TypeName, Precision: s.Precision, Scale: s.Scale}.TypeDeclaration()
	if err != nil {
		return "", fmt.Errorf("%s: %w", s.FQTN(), err)
	}

	cacheClause := "NO CACHE"
	if s.IsCached {
		cacheClause = fmt.Sprintf("CACHE %d", s.CacheSize)
	}
	cycleClause := "NO CYCLE"
	if s.IsCycling {
		cycleClause = "CYCLE"
	}

	createSQL := fmt.Sprintf(
		"CREATE SEQUENCE %s AS %s START WITH %s INCREMENT BY %s MINVALUE %s MAXVALUE %s %s %s;",
		s.FQTN(),
		typeDecl,
		s.RestartWith,
		s.Increment,
		s.MinValue,
		s.MaxValue,
		cycleClause,
		cacheClause,
	)
	alterSQL := fmt.Sprintf(
		"ALTER SEQUENCE %s RESTART WITH %s INCREMENT BY %s MINVALUE %s MAXVALUE %s %s %s;",
		s.FQTN(),
		s.RestartWith,
		s.Increment,
		s.MinValue,
		s.MaxValue,
		cycleClause,
		cacheClause,
	)

	return fmt.Sprintf("IF OBJECT_ID(N'%s', 'SO') IS NULL\nBEGIN\n    %s\nEND\nELSE\nBEGIN\n    %s\nEND", escapeSQLString(s.FQTN()), createSQL, alterSQL), nil
}

func (p procedureMeta) CreateProcedureSQL() string {
	definition := normalizeProgrammableDefinition(p.Definition, procedureHeaderPattern, procedureHeaderSearchPattern)
	return fmt.Sprintf("CREATE OR ALTER PROCEDURE %s %s", p.FQTN(), definition)
}

func (f functionMeta) CreateFunctionSQL() string {
	definition := normalizeProgrammableDefinition(f.Definition, functionHeaderPattern, functionHeaderSearchPattern)
	return fmt.Sprintf("CREATE OR ALTER FUNCTION %s %s", f.FQTN(), definition)
}

func (t triggerMeta) CreateTriggerSQL() string {
	definition := normalizeProgrammableDefinition(t.Definition, triggerHeaderPattern, triggerHeaderSearchPattern)
	return fmt.Sprintf("CREATE OR ALTER TRIGGER %s %s", t.FQTN(), definition)
}

func (s synonymMeta) CreateSynonymSQL() string {
	return fmt.Sprintf(
		"IF OBJECT_ID(N'%s', 'SN') IS NOT NULL DROP SYNONYM %s;\nCREATE SYNONYM %s FOR %s;",
		escapeSQLString(s.FQTN()),
		s.FQTN(),
		s.FQTN(),
		s.BaseObjectName,
	)
}
