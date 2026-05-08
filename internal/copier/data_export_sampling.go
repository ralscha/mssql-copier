package copier

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

type exportRowFetcher func(context.Context, tableMeta, []string, [][]any) ([]exportRow, error)

type pendingExportFetch struct {
	table     tableMeta
	columns   []string
	tupleKeys map[string][]any
}

type queuedExportRow struct {
	tableKey string
	row      exportRow
}

func resolveSampledExportRows(ctx context.Context, tables []tableMeta, baseRows map[string][]exportRow, fetcher exportRowFetcher) (map[string][]exportRow, error) {
	tableByKey := make(map[string]tableMeta, len(tables))
	rowsByTable := make(map[string][]exportRow, len(tables))
	rowKeysByTable := make(map[string]map[string]struct{}, len(tables))
	queue := make([]queuedExportRow, 0)

	for _, table := range tables {
		tableKey := normalizedTableKey(table)
		tableByKey[tableKey] = table
		rowKeysByTable[tableKey] = make(map[string]struct{})
		for _, row := range baseRows[tableKey] {
			rows := rowsByTable[tableKey]
			if appendUniqueExportRow(table, &rows, rowKeysByTable[tableKey], row) {
				rowsByTable[tableKey] = rows
				queue = append(queue, queuedExportRow{tableKey: tableKey, row: row})
			}
		}
	}

	for len(queue) > 0 {
		pending := make(map[string]*pendingExportFetch)
		for _, item := range queue {
			table := tableByKey[item.tableKey]
			for _, fk := range table.ForeignKeys {
				parentKey := normalizeFilterName(fk.RefSchema) + "." + normalizeFilterName(fk.RefTable)
				parent, ok := tableByKey[parentKey]
				if !ok {
					continue
				}

				tupleValues, tupleKey, ok := exportRowTuple(table, item.row, fk.Columns)
				if !ok {
					continue
				}
				if exportRowsContainTuple(rowsByTable[parentKey], parent, fk.RefColumns, tupleKey) {
					continue
				}

				requestKey := parentKey + "|" + normalizedColumnListKey(fk.RefColumns)
				request, ok := pending[requestKey]
				if !ok {
					request = &pendingExportFetch{
						table:     parent,
						columns:   append([]string(nil), fk.RefColumns...),
						tupleKeys: make(map[string][]any),
					}
					pending[requestKey] = request
				}
				request.tupleKeys[tupleKey] = tupleValues
			}
		}

		if len(pending) == 0 {
			break
		}

		requestKeys := make([]string, 0, len(pending))
		for requestKey := range pending {
			requestKeys = append(requestKeys, requestKey)
		}
		sort.Strings(requestKeys)

		nextQueue := make([]queuedExportRow, 0)
		for _, requestKey := range requestKeys {
			request := pending[requestKey]
			tupleKeys := make([]string, 0, len(request.tupleKeys))
			for tupleKey := range request.tupleKeys {
				tupleKeys = append(tupleKeys, tupleKey)
			}
			sort.Strings(tupleKeys)

			tuples := make([][]any, 0, len(tupleKeys))
			for _, tupleKey := range tupleKeys {
				tuples = append(tuples, request.tupleKeys[tupleKey])
			}

			fetchedRows, err := fetcher(ctx, request.table, request.columns, tuples)
			if err != nil {
				return nil, err
			}
			tableKey := normalizedTableKey(request.table)
			for _, row := range fetchedRows {
				rows := rowsByTable[tableKey]
				if appendUniqueExportRow(request.table, &rows, rowKeysByTable[tableKey], row) {
					rowsByTable[tableKey] = rows
					nextQueue = append(nextQueue, queuedExportRow{tableKey: tableKey, row: row})
				}
			}
		}
		queue = nextQueue
	}

	return rowsByTable, nil
}

func appendUniqueExportRow(table tableMeta, rows *[]exportRow, rowKeys map[string]struct{}, row exportRow) bool {
	rowKey := exportRowIdentity(table, row)
	if _, ok := rowKeys[rowKey]; ok {
		return false
	}
	rowKeys[rowKey] = struct{}{}
	*rows = append(*rows, row)
	return true
}

func exportRowsContainTuple(rows []exportRow, table tableMeta, columns []string, wantKey string) bool {
	for _, row := range rows {
		_, tupleKey, ok := exportRowTuple(table, row, columns)
		if ok && tupleKey == wantKey {
			return true
		}
	}
	return false
}

func exportRowTuple(table tableMeta, row exportRow, columns []string) ([]any, string, bool) {
	indexByName := exportColumnIndex(table)
	values := make([]any, 0, len(columns))
	parts := make([]string, 0, len(columns))
	for _, columnName := range columns {
		idx, ok := indexByName[strings.ToLower(columnName)]
		if !ok || idx >= len(row.values) {
			return nil, "", false
		}
		value := row.values[idx]
		if value == nil {
			return nil, "", false
		}
		values = append(values, value)
		parts = append(parts, exportValueKey(value))
	}
	return values, strings.Join(parts, "|"), true
}

func exportRowIdentity(table tableMeta, row exportRow) string {
	if table.PrimaryKey != nil && len(table.PrimaryKey.Columns) > 0 {
		primaryKeyCols := make([]string, 0, len(table.PrimaryKey.Columns))
		for _, col := range table.PrimaryKey.Columns {
			primaryKeyCols = append(primaryKeyCols, col.Name)
		}
		if _, key, ok := exportRowTuple(table, row, primaryKeyCols); ok {
			return "pk|" + key
		}
	}

	parts := make([]string, 0, len(row.values))
	for _, value := range row.values {
		parts = append(parts, exportValueKey(value))
	}
	return "row|" + strings.Join(parts, "|")
}

func exportColumnIndex(table tableMeta) map[string]int {
	indexByName := make(map[string]int, len(table.CopyColumns))
	for i, col := range table.CopyColumns {
		indexByName[strings.ToLower(col.Name)] = i
	}
	return indexByName
}

func exportValueKey(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case []byte:
		return "bytes:" + hex.EncodeToString(v)
	case string:
		return "string:" + v
	case bool:
		if v {
			return "bool:true"
		}
		return "bool:false"
	case time.Time:
		return "time:" + v.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprintf("%T:%v", v, v)
	}
}

func normalizedTableKey(table tableMeta) string {
	return normalizeFilterName(table.Schema) + "." + normalizeFilterName(table.Name)
}

func normalizedColumnListKey(columns []string) string {
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		parts = append(parts, normalizeFilterName(column))
	}
	return strings.Join(parts, ",")
}
