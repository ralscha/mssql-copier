package copier

import (
	"context"
	"fmt"
	"strings"
)

func (c *copier) loadMetadata(ctx context.Context) ([]tableMeta, error) {
	tables, err := c.loadTableCatalog(ctx)
	if err != nil {
		return nil, err
	}

	selected := tables[:0]
	for i := range tables {
		if !c.shouldCopyTable(tables[i].Schema, tables[i].Name) {
			continue
		}
		if err := c.loadTableDetails(ctx, &tables[i]); err != nil {
			return nil, fmt.Errorf("load %s metadata: %w", tables[i].FQTN(), err)
		}
		selected = append(selected, tables[i])
	}
	return selected, nil
}

func (c *copier) loadTableCatalog(ctx context.Context) ([]tableMeta, error) {
	const tablesSQL = `
SELECT
	s.name,
	t.name,
	t.object_id,
	COALESCE(SUM(CASE WHEN p.index_id IN (0, 1) THEN p.rows ELSE 0 END), 0) AS approx_rows
FROM sys.tables t
JOIN sys.schemas s ON s.schema_id = t.schema_id
LEFT JOIN sys.partitions p ON p.object_id = t.object_id
WHERE t.is_ms_shipped = 0
GROUP BY s.name, t.name, t.object_id
ORDER BY approx_rows DESC, s.name, t.name;`

	rows, err := c.sourceDB.QueryContext(ctx, tablesSQL)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "table metadata rows")

	var tables []tableMeta
	for rows.Next() {
		var table tableMeta
		if err := rows.Scan(&table.Schema, &table.Name, &table.ObjectID, &table.ApproxRows); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tables, nil
}

func (c *copier) loadTableDetails(ctx context.Context, table *tableMeta) error {
	columns, err := c.loadColumns(ctx, table.ObjectID)
	if err != nil {
		return err
	}
	table.Columns = columns
	table.PrimaryKey, err = c.loadPrimaryKey(ctx, table.ObjectID)
	if err != nil {
		return err
	}
	table.Checks, err = c.loadChecks(ctx, table.ObjectID)
	if err != nil {
		return err
	}
	table.ForeignKeys, err = c.loadForeignKeys(ctx, table.ObjectID)
	if err != nil {
		return err
	}
	table.Indexes, err = c.loadIndexes(ctx, table.ObjectID)
	if err != nil {
		return err
	}

	var copyColumns []columnMeta
	table.BulkOK = true
	for _, column := range table.Columns {
		if column.Identity {
			table.HasIdentity = true
		}
		if column.Copyable {
			copyColumns = append(copyColumns, column)
			if !supportsBulkType(column) {
				table.BulkOK = false
				if table.BulkReason == "" {
					table.BulkReason = fmt.Sprintf("column %s uses unsupported bulk type %s", column.Name, column.SystemTypeName)
				}
			}
		}
	}
	table.CopyColumns = copyColumns
	return nil
}

func (c *copier) loadColumns(ctx context.Context, objectID int) ([]columnMeta, error) {
	const sqlText = `
SELECT
	c.name,
	uts.name AS type_schema,
	ut.name AS user_type_name,
	st.name AS system_type_name,
	ut.is_user_defined,
	c.max_length,
	c.precision,
	c.scale,
	c.is_nullable,
	c.is_identity,
	COALESCE(CONVERT(nvarchar(100), ic.seed_value), ''),
	COALESCE(CONVERT(nvarchar(100), ic.increment_value), ''),
	c.is_computed,
	COALESCE(cc.definition, ''),
	COALESCE(cc.is_persisted, 0),
	COALESCE(dc.definition, ''),
	COALESCE(c.collation_name, ''),
	c.is_rowguidcol,
	CASE WHEN COLUMNPROPERTY(c.object_id, c.name, 'IsSparse') = 1 THEN 1 ELSE 0 END,
	CASE WHEN COLUMNPROPERTY(c.object_id, c.name, 'IsHidden') = 1 THEN 1 ELSE 0 END,
	COALESCE(c.generated_always_type, 0)
FROM sys.columns c
JOIN sys.types ut ON ut.user_type_id = c.user_type_id
JOIN sys.schemas uts ON uts.schema_id = ut.schema_id
JOIN sys.types st ON st.user_type_id = c.system_type_id AND st.user_type_id = st.system_type_id
LEFT JOIN sys.identity_columns ic ON ic.object_id = c.object_id AND ic.column_id = c.column_id
LEFT JOIN sys.computed_columns cc ON cc.object_id = c.object_id AND cc.column_id = c.column_id
LEFT JOIN sys.default_constraints dc ON dc.object_id = c.default_object_id
WHERE c.object_id = @p1
ORDER BY c.column_id;`

	rows, err := c.sourceDB.QueryContext(ctx, sqlText, objectID)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "column metadata rows")

	var columns []columnMeta
	for rows.Next() {
		var col columnMeta
		if err := rows.Scan(
			&col.Name,
			&col.TypeSchema,
			&col.UserTypeName,
			&col.SystemTypeName,
			&col.IsUserDefined,
			&col.MaxLength,
			&col.Precision,
			&col.Scale,
			&col.Nullable,
			&col.Identity,
			&col.IdentitySeed,
			&col.IdentityIncrement,
			&col.Computed,
			&col.ComputedDefinition,
			&col.ComputedPersisted,
			&col.DefaultDefinition,
			&col.Collation,
			&col.RowGuidCol,
			&col.Sparse,
			&col.Hidden,
			&col.GeneratedAlways,
		); err != nil {
			return nil, err
		}
		col.SystemTypeName = strings.ToLower(col.SystemTypeName)
		col.UserTypeName = strings.ToLower(col.UserTypeName)
		col.TypeSchema = strings.ToLower(col.TypeSchema)

		switch {
		case col.GeneratedAlways != 0:
			return nil, fmt.Errorf("unsupported generated-always column %s; temporal/system-versioned tables are not supported", col.Name)
		case col.Computed:
			col.Copyable = false
			col.SkipReason = "computed"
		case col.Hidden:
			col.Copyable = false
			col.SkipReason = "hidden"
		case col.SystemTypeName == "timestamp" || col.SystemTypeName == "rowversion":
			col.Copyable = false
			col.SkipReason = "rowversion is regenerated by SQL Server"
		default:
			col.Copyable = true
		}
		columns = append(columns, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func (c *copier) loadPrimaryKey(ctx context.Context, objectID int) (*keyConstraint, error) {
	const sqlText = `
SELECT
	kc.name,
	i.type_desc,
	ic.key_ordinal,
	c.name,
	ic.is_descending_key
FROM sys.key_constraints kc
JOIN sys.indexes i ON i.object_id = kc.parent_object_id AND i.index_id = kc.unique_index_id
JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
WHERE kc.parent_object_id = @p1 AND kc.type = 'PK'
ORDER BY ic.key_ordinal;`

	rows, err := c.sourceDB.QueryContext(ctx, sqlText, objectID)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "primary key rows")

	var pk *keyConstraint
	for rows.Next() {
		if pk == nil {
			pk = &keyConstraint{Kind: "PRIMARY KEY"}
		}
		var ordinal int
		var col keyColumn
		if err := rows.Scan(&pk.Name, &pk.Cluster, &ordinal, &col.Name, &col.Desc); err != nil {
			return nil, err
		}
		pk.Columns = append(pk.Columns, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return pk, nil
}

func (c *copier) loadChecks(ctx context.Context, objectID int) ([]checkConstraint, error) {
	const sqlText = `
SELECT name, definition, CASE WHEN is_not_trusted = 1 THEN 0 ELSE 1 END, is_disabled
FROM sys.check_constraints
WHERE parent_object_id = @p1
ORDER BY name;`

	rows, err := c.sourceDB.QueryContext(ctx, sqlText, objectID)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "check constraint rows")

	var checks []checkConstraint
	for rows.Next() {
		var check checkConstraint
		if err := rows.Scan(&check.Name, &check.Definition, &check.Trusted, &check.Disabled); err != nil {
			return nil, err
		}
		checks = append(checks, check)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return checks, nil
}

func (c *copier) loadForeignKeys(ctx context.Context, objectID int) ([]foreignKey, error) {
	const sqlText = `
SELECT
	fk.name,
	fkc.constraint_column_id,
	pc.name,
	rs.name,
	rt.name,
	rc.name,
	fk.delete_referential_action_desc,
	fk.update_referential_action_desc,
	CASE WHEN fk.is_not_trusted = 1 THEN 0 ELSE 1 END,
	fk.is_disabled
FROM sys.foreign_keys fk
JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id
JOIN sys.tables rt ON rt.object_id = fkc.referenced_object_id
JOIN sys.schemas rs ON rs.schema_id = rt.schema_id
JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id
WHERE fk.parent_object_id = @p1
ORDER BY fk.name, fkc.constraint_column_id;`

	rows, err := c.sourceDB.QueryContext(ctx, sqlText, objectID)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "foreign key rows")

	var fks []foreignKey
	byName := map[string]int{}
	for rows.Next() {
		var (
			name         string
			ordinal      int
			column       string
			refSchema    string
			refTable     string
			refColumn    string
			deleteAction string
			updateAction string
			trusted      bool
			disabled     bool
		)
		if err := rows.Scan(&name, &ordinal, &column, &refSchema, &refTable, &refColumn, &deleteAction, &updateAction, &trusted, &disabled); err != nil {
			return nil, err
		}
		idx, ok := byName[name]
		if !ok {
			fks = append(fks, foreignKey{
				Name:         name,
				RefSchema:    refSchema,
				RefTable:     refTable,
				DeleteAction: deleteAction,
				UpdateAction: updateAction,
				Trusted:      trusted,
				Disabled:     disabled,
			})
			idx = len(fks) - 1
			byName[name] = idx
		}
		fks[idx].Columns = append(fks[idx].Columns, column)
		fks[idx].RefColumns = append(fks[idx].RefColumns, refColumn)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return fks, nil
}

func (c *copier) loadIndexes(ctx context.Context, objectID int) ([]indexMeta, error) {
	const sqlText = `
SELECT
	i.name,
	i.is_unique,
	i.type_desc,
	COALESCE(i.filter_definition, ''),
	ic.key_ordinal,
	ic.is_descending_key,
	ic.is_included_column,
	c.name
FROM sys.indexes i
JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
WHERE i.object_id = @p1
	AND i.name IS NOT NULL
	AND i.is_primary_key = 0
	AND i.is_hypothetical = 0
	AND i.type IN (1, 2)
ORDER BY i.name, ic.is_included_column, ic.key_ordinal, ic.index_column_id;`

	rows, err := c.sourceDB.QueryContext(ctx, sqlText, objectID)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "index rows")

	var indexes []indexMeta
	byName := map[string]int{}
	for rows.Next() {
		var (
			name       string
			unique     bool
			cluster    string
			filter     string
			ordinal    int
			desc       bool
			included   bool
			columnName string
		)
		if err := rows.Scan(&name, &unique, &cluster, &filter, &ordinal, &desc, &included, &columnName); err != nil {
			return nil, err
		}
		idx, ok := byName[name]
		if !ok {
			indexes = append(indexes, indexMeta{Name: name, Unique: unique, Cluster: cluster, Filter: filter})
			idx = len(indexes) - 1
			byName[name] = idx
		}
		if included {
			indexes[idx].Include = append(indexes[idx].Include, columnName)
		} else {
			indexes[idx].KeyColumns = append(indexes[idx].KeyColumns, keyColumn{Name: columnName, Desc: desc})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return indexes, nil
}

func (c *copier) loadViews(ctx context.Context) ([]viewMeta, error) {
	const viewsSQL = `
SELECT
	s.name,
	v.name,
	OBJECT_DEFINITION(v.object_id)
FROM sys.views v
JOIN sys.schemas s ON s.schema_id = v.schema_id
WHERE v.is_ms_shipped = 0
ORDER BY s.name, v.name;`

	rows, err := c.sourceDB.QueryContext(ctx, viewsSQL)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "view rows")

	var views []viewMeta
	for rows.Next() {
		var v viewMeta
		var def *string
		if err := rows.Scan(&v.Schema, &v.Name, &def); err != nil {
			return nil, err
		}
		if def == nil {
			continue
		}
		v.Definition = *def
		views = append(views, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return views, nil
}

func (c *copier) loadAliasTypes(ctx context.Context) ([]aliasTypeMeta, error) {
	const sqlText = `
SELECT
	s.name,
	t.name,
	st.name,
	t.max_length,
	t.precision,
	t.scale,
	t.is_nullable
FROM sys.types t
JOIN sys.schemas s ON s.schema_id = t.schema_id
JOIN sys.types st ON st.user_type_id = t.system_type_id AND st.user_type_id = st.system_type_id
WHERE t.is_user_defined = 1
	AND t.is_assembly_type = 0
	AND t.is_table_type = 0
ORDER BY s.name, t.name;`

	rows, err := c.sourceDB.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "alias type rows")

	var aliasTypes []aliasTypeMeta
	for rows.Next() {
		var aliasType aliasTypeMeta
		if err := rows.Scan(
			&aliasType.Schema,
			&aliasType.Name,
			&aliasType.SystemTypeName,
			&aliasType.MaxLength,
			&aliasType.Precision,
			&aliasType.Scale,
			&aliasType.Nullable,
		); err != nil {
			return nil, err
		}
		aliasType.SystemTypeName = strings.ToLower(aliasType.SystemTypeName)
		aliasTypes = append(aliasTypes, aliasType)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return aliasTypes, nil
}

func (c *copier) loadTableTypes(ctx context.Context) ([]tableTypeMeta, error) {
	const sqlText = `
SELECT
	s.name,
	tt.name,
	tt.type_table_object_id
FROM sys.table_types tt
JOIN sys.schemas s ON s.schema_id = tt.schema_id
ORDER BY s.name, tt.name;`

	rows, err := c.sourceDB.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "table type rows")

	var tableTypes []tableTypeMeta
	for rows.Next() {
		var tableType tableTypeMeta
		if err := rows.Scan(&tableType.Schema, &tableType.Name, &tableType.ObjectID); err != nil {
			return nil, err
		}
		if err := c.loadTableTypeDetails(ctx, &tableType); err != nil {
			return nil, fmt.Errorf("load %s metadata: %w", tableType.FQTN(), err)
		}
		tableTypes = append(tableTypes, tableType)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tableTypes, nil
}

func (c *copier) loadTableTypeDetails(ctx context.Context, tableType *tableTypeMeta) error {
	columns, err := c.loadColumns(ctx, tableType.ObjectID)
	if err != nil {
		return err
	}
	tableType.Columns = columns
	tableType.PrimaryKey, err = c.loadPrimaryKey(ctx, tableType.ObjectID)
	if err != nil {
		return err
	}
	tableType.Checks, err = c.loadChecks(ctx, tableType.ObjectID)
	if err != nil {
		return err
	}
	return nil
}

func (c *copier) loadSequences(ctx context.Context) ([]sequenceMeta, error) {
	const sqlText = `
SELECT
	s.name,
	seq.name,
	t.name,
	seq.precision,
	seq.scale,
	CONVERT(nvarchar(100), seq.start_value),
	CONVERT(nvarchar(100), seq.increment),
	CONVERT(nvarchar(100), seq.minimum_value),
	CONVERT(nvarchar(100), seq.maximum_value),
	CASE
		WHEN seq.last_used_value IS NULL THEN CONVERT(nvarchar(100), seq.start_value)
		ELSE CONVERT(nvarchar(100), CONVERT(decimal(38, 0), seq.last_used_value) + CONVERT(decimal(38, 0), seq.increment))
	END,
	seq.is_cycling,
	seq.is_cached,
	COALESCE(seq.cache_size, 0)
FROM sys.sequences seq
JOIN sys.schemas s ON s.schema_id = seq.schema_id
JOIN sys.types t ON t.user_type_id = seq.user_type_id
WHERE seq.is_ms_shipped = 0
ORDER BY s.name, seq.name;`

	rows, err := c.sourceDB.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "sequence rows")

	var sequences []sequenceMeta
	for rows.Next() {
		var seq sequenceMeta
		if err := rows.Scan(
			&seq.Schema,
			&seq.Name,
			&seq.TypeName,
			&seq.Precision,
			&seq.Scale,
			&seq.StartValue,
			&seq.Increment,
			&seq.MinValue,
			&seq.MaxValue,
			&seq.RestartWith,
			&seq.IsCycling,
			&seq.IsCached,
			&seq.CacheSize,
		); err != nil {
			return nil, err
		}
		seq.TypeName = strings.ToLower(seq.TypeName)
		sequences = append(sequences, seq)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sequences, nil
}

func (c *copier) loadProcedures(ctx context.Context) ([]procedureMeta, error) {
	const sqlText = `
SELECT
	s.name,
	p.name,
	OBJECT_DEFINITION(p.object_id)
FROM sys.procedures p
JOIN sys.schemas s ON s.schema_id = p.schema_id
WHERE p.is_ms_shipped = 0
ORDER BY s.name, p.name;`

	rows, err := c.sourceDB.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "procedure rows")

	var procedures []procedureMeta
	for rows.Next() {
		var proc procedureMeta
		var def *string
		if err := rows.Scan(&proc.Schema, &proc.Name, &def); err != nil {
			return nil, err
		}
		if def == nil {
			continue
		}
		proc.Definition = *def
		procedures = append(procedures, proc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return procedures, nil
}

func (c *copier) loadTriggers(ctx context.Context) ([]triggerMeta, error) {
	const sqlText = `
SELECT
	OBJECT_SCHEMA_NAME(tr.object_id),
	tr.name,
	ts.name,
	tbl.name,
	tr.is_disabled,
	OBJECT_DEFINITION(tr.object_id)
FROM sys.triggers tr
JOIN sys.tables tbl ON tbl.object_id = tr.parent_id
JOIN sys.schemas ts ON ts.schema_id = tbl.schema_id
WHERE tr.parent_class = 1
	AND tr.is_ms_shipped = 0
ORDER BY OBJECT_SCHEMA_NAME(tr.object_id), tr.name;`

	rows, err := c.sourceDB.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "trigger rows")

	var triggers []triggerMeta
	for rows.Next() {
		var trigger triggerMeta
		var def *string
		if err := rows.Scan(&trigger.Schema, &trigger.Name, &trigger.TableSchema, &trigger.TableName, &trigger.Disabled, &def); err != nil {
			return nil, err
		}
		if def == nil {
			continue
		}
		if !c.shouldCopyTable(trigger.TableSchema, trigger.TableName) {
			continue
		}
		trigger.Definition = *def
		triggers = append(triggers, trigger)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return triggers, nil
}

func (c *copier) loadFunctions(ctx context.Context) ([]functionMeta, error) {
	const sqlText = `
SELECT
	s.name,
	o.name,
	o.type,
	OBJECT_DEFINITION(o.object_id)
FROM sys.objects o
JOIN sys.schemas s ON s.schema_id = o.schema_id
WHERE o.is_ms_shipped = 0
	AND o.type IN ('FN', 'IF', 'TF')
ORDER BY s.name, o.name;`

	rows, err := c.sourceDB.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "function rows")

	var functions []functionMeta
	for rows.Next() {
		var fn functionMeta
		var def *string
		if err := rows.Scan(&fn.Schema, &fn.Name, &fn.Kind, &def); err != nil {
			return nil, err
		}
		if def == nil {
			continue
		}
		fn.Definition = *def
		functions = append(functions, fn)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return functions, nil
}

func (c *copier) loadSynonyms(ctx context.Context) ([]synonymMeta, error) {
	const sqlText = `
SELECT
	s.name,
	syn.name,
	syn.base_object_name
FROM sys.synonyms syn
JOIN sys.schemas s ON s.schema_id = syn.schema_id
ORDER BY s.name, syn.name;`

	rows, err := c.sourceDB.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "synonym rows")

	var synonyms []synonymMeta
	for rows.Next() {
		var syn synonymMeta
		if err := rows.Scan(&syn.Schema, &syn.Name, &syn.BaseObjectName); err != nil {
			return nil, err
		}
		synonyms = append(synonyms, syn)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return synonyms, nil
}

func (v viewMeta) FQTN() string {
	return quoteIdent(v.Schema) + "." + quoteIdent(v.Name)
}

func (s sequenceMeta) FQTN() string {
	return quoteIdent(s.Schema) + "." + quoteIdent(s.Name)
}

func (a aliasTypeMeta) FQTN() string {
	return quoteIdent(a.Schema) + "." + quoteIdent(a.Name)
}

func (t tableTypeMeta) FQTN() string {
	return quoteIdent(t.Schema) + "." + quoteIdent(t.Name)
}

func (t triggerMeta) FQTN() string {
	return quoteIdent(t.Schema) + "." + quoteIdent(t.Name)
}

func (t triggerMeta) TableFQTN() string {
	return quoteIdent(t.TableSchema) + "." + quoteIdent(t.TableName)
}

func (p procedureMeta) FQTN() string {
	return quoteIdent(p.Schema) + "." + quoteIdent(p.Name)
}

func (f functionMeta) FQTN() string {
	return quoteIdent(f.Schema) + "." + quoteIdent(f.Name)
}

func (s synonymMeta) FQTN() string {
	return quoteIdent(s.Schema) + "." + quoteIdent(s.Name)
}

func (c *copier) resolveProgrammableDependencies(ctx context.Context, referencingFQTN string, candidateFQTNs map[string]struct{}) ([]string, error) {
	const sqlText = `
SELECT
	COALESCE(d.referenced_schema_name, OBJECT_SCHEMA_NAME(d.referenced_id)),
	COALESCE(d.referenced_entity_name, OBJECT_NAME(d.referenced_id))
FROM sys.sql_expression_dependencies d
WHERE d.referencing_id = OBJECT_ID(@p1)
	AND d.referenced_id IS NOT NULL
	AND d.referenced_class_desc = 'OBJECT_OR_COLUMN';`

	rows, err := c.sourceDB.QueryContext(ctx, sqlText, referencingFQTN)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "programmable dependency rows")

	var deps []string
	seen := map[string]struct{}{}
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			return nil, err
		}
		depFQTN := strings.ToLower(quoteIdent(schema) + "." + quoteIdent(name))
		if _, ok := candidateFQTNs[depFQTN]; ok {
			if _, ok := seen[depFQTN]; ok {
				continue
			}
			seen[depFQTN] = struct{}{}
			deps = append(deps, depFQTN)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return deps, nil
}
