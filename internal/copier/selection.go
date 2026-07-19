package copier

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sort"
	"strings"
)

type discoveredObject struct {
	schema string
	name   string
	kind   string
	index  int
}

func (c *copier) selectDependencyClosure(ctx context.Context) error {
	objects := c.discoveredObjects()
	available := make(map[string]struct{}, len(objects))
	for key := range objects {
		available[key] = struct{}{}
	}

	dependencies := make(map[string][]string, len(objects))
	resolved := make(map[string]struct{}, len(objects))
	selected := make(map[string]struct{}, len(objects))
	dataSelected := make(map[string]struct{}, len(c.tables))
	queue := make([]string, 0, len(objects))
	selectObject := func(key string) {
		if _, ok := selected[key]; ok {
			return
		}
		selected[key] = struct{}{}
		queue = append(queue, key)
	}
	var rootKeys []string
	for key, object := range objects {
		if c.shouldCopyTable(object.schema, object.name) {
			rootKeys = append(rootKeys, key)
		}
	}
	sort.Strings(rootKeys)
	for _, key := range rootKeys {
		selectObject(key)
		if objects[key].kind == "table" {
			dataSelected[key] = struct{}{}
		}
	}
	for _, trigger := range c.triggers {
		if _, ok := selected[objectKey(trigger.TableSchema, trigger.TableName)]; ok && !c.isExplicitlyExcluded(trigger.Schema, trigger.Name) {
			selectObject(objectKey(trigger.Schema, trigger.Name))
		}
	}

	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if _, ok := resolved[key]; !ok {
			object := objects[key]
			deps, err := c.resolveObjectDependencies(ctx, object, available)
			if err != nil {
				return err
			}
			dependencies[key] = deps
			resolved[key] = struct{}{}
		}
		for _, dependency := range dependencies[key] {
			object, ok := objects[dependency]
			if !ok {
				continue
			}
			if c.isExplicitlyExcluded(object.schema, object.name) {
				return fmt.Errorf("selected %s depends on excluded %s %s", key, object.kind, dependency)
			}
			selectObject(dependency)
		}
	}

	c.tables = filterTables(c.tables, selected, dataSelected)
	c.aliasTypes = filterAliasTypes(c.aliasTypes, selected)
	c.tableTypes = filterTableTypes(c.tableTypes, selected)
	c.sequences = filterSequences(c.sequences, selected)
	c.views = filterViews(c.views, selected)
	c.functions = filterFunctions(c.functions, selected)
	c.procedures = filterProcedures(c.procedures, selected)
	c.triggers = filterTriggers(c.triggers, selected)
	c.synonyms = filterSynonyms(c.synonyms, selected)
	return nil
}

func (c *copier) resolveObjectDependencies(ctx context.Context, object discoveredObject, candidates map[string]struct{}) ([]string, error) {
	switch object.kind {
	case "table":
		table := &c.tables[object.index]
		if err := c.loadTableDetails(ctx, table); err != nil {
			return nil, fmt.Errorf("load %s metadata: %w", table.FQTN(), err)
		}
		var deps []string
		for _, col := range table.Columns {
			if col.IsUserDefined {
				deps = appendUniqueDependency(deps, objectKey(col.TypeSchema, col.UserTypeName))
			}
		}
		resolved, err := c.resolveTableExpressionDependencies(ctx, *table, candidates)
		if err != nil {
			return nil, fmt.Errorf("resolve dependencies for %s: %w", table.FQTN(), err)
		}
		deps = appendUniqueDependencies(deps, resolved)
		table.DependsOn = deps
		return deps, nil
	case "table type":
		var deps []string
		for _, col := range c.tableTypes[object.index].Columns {
			if col.IsUserDefined {
				deps = appendUniqueDependency(deps, objectKey(col.TypeSchema, col.UserTypeName))
			}
		}
		return deps, nil
	case "view":
		item := &c.views[object.index]
		deps, err := c.resolveProgrammableDependencies(ctx, item.FQTN(), candidates)
		if err != nil {
			return nil, fmt.Errorf("resolve dependencies for %s: %w", item.FQTN(), err)
		}
		item.DependsOn = deps
		return deps, nil
	case "function":
		item := &c.functions[object.index]
		deps, err := c.resolveProgrammableDependencies(ctx, item.FQTN(), candidates)
		if err != nil {
			return nil, fmt.Errorf("resolve dependencies for %s: %w", item.FQTN(), err)
		}
		parameterDeps, err := c.resolveParameterTypeDependencies(ctx, item.FQTN(), candidates)
		if err != nil {
			return nil, fmt.Errorf("resolve parameter types for %s: %w", item.FQTN(), err)
		}
		item.DependsOn = appendUniqueDependencies(deps, parameterDeps)
		return item.DependsOn, nil
	case "procedure":
		item := &c.procedures[object.index]
		deps, err := c.resolveProgrammableDependencies(ctx, item.FQTN(), candidates)
		if err != nil {
			return nil, fmt.Errorf("resolve dependencies for %s: %w", item.FQTN(), err)
		}
		parameterDeps, err := c.resolveParameterTypeDependencies(ctx, item.FQTN(), candidates)
		if err != nil {
			return nil, fmt.Errorf("resolve parameter types for %s: %w", item.FQTN(), err)
		}
		item.DependsOn = appendUniqueDependencies(deps, parameterDeps)
		return item.DependsOn, nil
	case "trigger":
		item := &c.triggers[object.index]
		deps, err := c.resolveProgrammableDependencies(ctx, item.FQTN(), candidates)
		if err != nil {
			return nil, fmt.Errorf("resolve dependencies for %s: %w", item.FQTN(), err)
		}
		item.DependsOn = deps
		return deps, nil
	default:
		return nil, nil
	}
}

func (c *copier) discoveredObjects() map[string]discoveredObject {
	objects := make(map[string]discoveredObject)
	add := func(schema, name, kind string, index int) {
		objects[objectKey(schema, name)] = discoveredObject{schema: schema, name: name, kind: kind, index: index}
	}
	for i, table := range c.tables {
		add(table.Schema, table.Name, "table", i)
	}
	for i, item := range c.aliasTypes {
		add(item.Schema, item.Name, "alias type", i)
	}
	for i, item := range c.tableTypes {
		add(item.Schema, item.Name, "table type", i)
	}
	for i, item := range c.sequences {
		add(item.Schema, item.Name, "sequence", i)
	}
	for i, item := range c.views {
		add(item.Schema, item.Name, "view", i)
	}
	for i, item := range c.functions {
		add(item.Schema, item.Name, "function", i)
	}
	for i, item := range c.procedures {
		add(item.Schema, item.Name, "procedure", i)
	}
	for i, item := range c.triggers {
		add(item.Schema, item.Name, "trigger", i)
	}
	for i, item := range c.synonyms {
		add(item.Schema, item.Name, "synonym", i)
	}
	return objects
}

func filterTables(values []tableMeta, selected map[string]struct{}, dataSelected map[string]struct{}) []tableMeta {
	result := values[:0]
	for _, value := range values {
		key := objectKey(value.Schema, value.Name)
		if _, ok := selected[key]; !ok {
			continue
		}
		_, copiesData := dataSelected[key]
		value.DependencyOnly = !copiesData
		result = append(result, value)
	}
	return result
}

func (c *copier) resolveTableExpressionDependencies(ctx context.Context, table tableMeta, candidates map[string]struct{}) ([]string, error) {
	const query = `
SELECT DISTINCT
	COALESCE(d.referenced_schema_name, OBJECT_SCHEMA_NAME(d.referenced_id)),
	COALESCE(d.referenced_entity_name, OBJECT_NAME(d.referenced_id))
FROM sys.sql_expression_dependencies d
LEFT JOIN sys.default_constraints dc ON dc.object_id = d.referencing_id
LEFT JOIN sys.check_constraints cc ON cc.object_id = d.referencing_id
WHERE d.referencing_id = @p1
	OR dc.parent_object_id = @p1
	OR cc.parent_object_id = @p1;`
	rows, err := c.sourceDB.QueryContext(ctx, query, table.ObjectID)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "table expression dependency rows")

	var dependencies []string
	for rows.Next() {
		var schemaName, objectName sql.NullString
		if err := rows.Scan(&schemaName, &objectName); err != nil {
			return nil, err
		}
		if !schemaName.Valid || !objectName.Valid {
			continue
		}
		key := objectKey(schemaName.String, objectName.String)
		if _, ok := candidates[key]; ok {
			dependencies = appendUniqueDependency(dependencies, key)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return dependencies, nil
}

func (c *copier) resolveParameterTypeDependencies(ctx context.Context, fqtn string, candidates map[string]struct{}) ([]string, error) {
	const query = `
SELECT DISTINCT s.name, t.name
FROM sys.parameters p
JOIN sys.types t ON t.user_type_id = p.user_type_id
JOIN sys.schemas s ON s.schema_id = t.schema_id
WHERE p.object_id = OBJECT_ID(@p1)
	AND t.is_user_defined = 1;`
	rows, err := c.sourceDB.QueryContext(ctx, query, fqtn)
	if err != nil {
		return nil, err
	}
	defer closeAndLog(rows, "parameter type dependency rows")

	var dependencies []string
	for rows.Next() {
		var schemaName, typeName string
		if err := rows.Scan(&schemaName, &typeName); err != nil {
			return nil, err
		}
		key := objectKey(schemaName, typeName)
		if _, ok := candidates[key]; ok {
			dependencies = appendUniqueDependency(dependencies, key)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return dependencies, nil
}

func objectKey(schema, name string) string {
	return strings.ToLower(quoteIdent(schema) + "." + quoteIdent(name))
}

func appendUniqueDependencies(existing []string, values []string) []string {
	for _, value := range values {
		existing = appendUniqueDependency(existing, value)
	}
	return existing
}

func appendUniqueDependency(existing []string, value string) []string {
	value = strings.ToLower(value)
	if slices.Contains(existing, value) {
		return existing
	}
	return append(existing, value)
}

func filterAliasTypes(values []aliasTypeMeta, selected map[string]struct{}) []aliasTypeMeta {
	result := values[:0]
	for _, value := range values {
		if _, ok := selected[objectKey(value.Schema, value.Name)]; ok {
			result = append(result, value)
		}
	}
	return result
}

func filterTableTypes(values []tableTypeMeta, selected map[string]struct{}) []tableTypeMeta {
	result := values[:0]
	for _, value := range values {
		if _, ok := selected[objectKey(value.Schema, value.Name)]; ok {
			result = append(result, value)
		}
	}
	return result
}

func filterSequences(values []sequenceMeta, selected map[string]struct{}) []sequenceMeta {
	result := values[:0]
	for _, value := range values {
		if _, ok := selected[objectKey(value.Schema, value.Name)]; ok {
			result = append(result, value)
		}
	}
	return result
}

func filterViews(values []viewMeta, selected map[string]struct{}) []viewMeta {
	result := values[:0]
	for _, value := range values {
		if _, ok := selected[objectKey(value.Schema, value.Name)]; ok {
			result = append(result, value)
		}
	}
	return result
}

func filterFunctions(values []functionMeta, selected map[string]struct{}) []functionMeta {
	result := values[:0]
	for _, value := range values {
		if _, ok := selected[objectKey(value.Schema, value.Name)]; ok {
			result = append(result, value)
		}
	}
	return result
}

func filterProcedures(values []procedureMeta, selected map[string]struct{}) []procedureMeta {
	result := values[:0]
	for _, value := range values {
		if _, ok := selected[objectKey(value.Schema, value.Name)]; ok {
			result = append(result, value)
		}
	}
	return result
}

func filterTriggers(values []triggerMeta, selected map[string]struct{}) []triggerMeta {
	result := values[:0]
	for _, value := range values {
		if _, ok := selected[objectKey(value.Schema, value.Name)]; ok {
			result = append(result, value)
		}
	}
	return result
}

func filterSynonyms(values []synonymMeta, selected map[string]struct{}) []synonymMeta {
	result := values[:0]
	for _, value := range values {
		if _, ok := selected[objectKey(value.Schema, value.Name)]; ok {
			result = append(result, value)
		}
	}
	return result
}
