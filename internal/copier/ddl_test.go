package copier

import (
	"strings"
	"testing"
)

func TestFQTN(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "orders"}
	got := table.FQTN()
	want := "[dbo].[orders]"
	if got != want {
		t.Fatalf("FQTN() = %q, want %q", got, want)
	}
}

func TestTypeDeclaration(t *testing.T) {
	tests := []struct {
		name string
		col  columnMeta
		want string
	}{
		{name: "varchar with length", col: columnMeta{SystemTypeName: "varchar", MaxLength: 50}, want: "varchar(50)"},
		{name: "varchar max", col: columnMeta{SystemTypeName: "varchar", MaxLength: -1}, want: "varchar(max)"},
		{name: "char", col: columnMeta{SystemTypeName: "char", MaxLength: 10}, want: "char(10)"},
		{name: "varbinary", col: columnMeta{SystemTypeName: "varbinary", MaxLength: 256}, want: "varbinary(256)"},
		{name: "binary", col: columnMeta{SystemTypeName: "binary", MaxLength: 16}, want: "binary(16)"},
		{name: "nvarchar", col: columnMeta{SystemTypeName: "nvarchar", MaxLength: 100}, want: "nvarchar(50)"},
		{name: "nvarchar max", col: columnMeta{SystemTypeName: "nvarchar", MaxLength: -1}, want: "nvarchar(max)"},
		{name: "nchar", col: columnMeta{SystemTypeName: "nchar", MaxLength: 20}, want: "nchar(10)"},
		{name: "decimal", col: columnMeta{SystemTypeName: "decimal", Precision: 18, Scale: 2}, want: "decimal(18,2)"},
		{name: "numeric", col: columnMeta{SystemTypeName: "numeric", Precision: 10, Scale: 4}, want: "numeric(10,4)"},
		{name: "datetime2", col: columnMeta{SystemTypeName: "datetime2", Scale: 7}, want: "datetime2(7)"},
		{name: "datetimeoffset", col: columnMeta{SystemTypeName: "datetimeoffset", Scale: 3}, want: "datetimeoffset(3)"},
		{name: "time", col: columnMeta{SystemTypeName: "time", Scale: 4}, want: "time(4)"},
		{name: "float default", col: columnMeta{SystemTypeName: "float", Precision: 53}, want: "float"},
		{name: "float zero precision", col: columnMeta{SystemTypeName: "float", Precision: 0}, want: "float"},
		{name: "float custom", col: columnMeta{SystemTypeName: "float", Precision: 24}, want: "float(24)"},
		{name: "timestamp", col: columnMeta{SystemTypeName: "timestamp"}, want: "rowversion"},
		{name: "rowversion", col: columnMeta{SystemTypeName: "rowversion"}, want: "rowversion"},
		{name: "bigint", col: columnMeta{SystemTypeName: "bigint"}, want: "bigint"},
		{name: "bit", col: columnMeta{SystemTypeName: "bit"}, want: "bit"},
		{name: "date", col: columnMeta{SystemTypeName: "date"}, want: "date"},
		{name: "datetime", col: columnMeta{SystemTypeName: "datetime"}, want: "datetime"},
		{name: "int", col: columnMeta{SystemTypeName: "int"}, want: "int"},
		{name: "money", col: columnMeta{SystemTypeName: "money"}, want: "money"},
		{name: "real", col: columnMeta{SystemTypeName: "real"}, want: "real"},
		{name: "smalldatetime", col: columnMeta{SystemTypeName: "smalldatetime"}, want: "smalldatetime"},
		{name: "smallint", col: columnMeta{SystemTypeName: "smallint"}, want: "smallint"},
		{name: "smallmoney", col: columnMeta{SystemTypeName: "smallmoney"}, want: "smallmoney"},
		{name: "text", col: columnMeta{SystemTypeName: "text"}, want: "text"},
		{name: "tinyint", col: columnMeta{SystemTypeName: "tinyint"}, want: "tinyint"},
		{name: "uniqueidentifier", col: columnMeta{SystemTypeName: "uniqueidentifier"}, want: "uniqueidentifier"},
		{name: "xml", col: columnMeta{SystemTypeName: "xml"}, want: "xml"},
		{name: "geography", col: columnMeta{SystemTypeName: "geography"}, want: "geography"},
		{name: "geometry", col: columnMeta{SystemTypeName: "geometry"}, want: "geometry"},
		{name: "hierarchyid", col: columnMeta{SystemTypeName: "hierarchyid"}, want: "hierarchyid"},
		{name: "image", col: columnMeta{SystemTypeName: "image"}, want: "image"},
		{name: "ntext", col: columnMeta{SystemTypeName: "ntext"}, want: "ntext"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.col.TypeDeclaration()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Fatalf("TypeDeclaration() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTypeDeclarationUnsupported(t *testing.T) {
	col := columnMeta{SystemTypeName: "unknown_type"}
	_, err := col.TypeDeclaration()
	if err == nil {
		t.Fatal("expected error for unsupported type, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported column type") {
		t.Fatalf("expected 'unsupported column type' in error, got: %v", err)
	}
}

func TestDefinitionSQL(t *testing.T) {
	tests := []struct {
		name    string
		col     columnMeta
		contain []string
	}{
		{
			name:    "basic nullable int",
			col:     columnMeta{Name: "id", SystemTypeName: "int", Nullable: true},
			contain: []string{"[id]", "int", "NULL"},
		},
		{
			name:    "not null varchar",
			col:     columnMeta{Name: "name", SystemTypeName: "varchar", MaxLength: 100, Nullable: false},
			contain: []string{"[name]", "varchar(100)", "NOT NULL"},
		},
		{
			name:    "identity column",
			col:     columnMeta{Name: "id", SystemTypeName: "int", Identity: true, IdentitySeed: "1", IdentityIncrement: "1", Nullable: false},
			contain: []string{"[id]", "int", "IDENTITY(1,1)", "NOT NULL"},
		},
		{
			name:    "identity custom seed",
			col:     columnMeta{Name: "id", SystemTypeName: "bigint", Identity: true, IdentitySeed: "1000", IdentityIncrement: "5", Nullable: false},
			contain: []string{"[id]", "bigint", "IDENTITY(1000,5)", "NOT NULL"},
		},
		{
			name:    "identity empty seed/increment",
			col:     columnMeta{Name: "id", SystemTypeName: "int", Identity: true, IdentitySeed: "", IdentityIncrement: "", Nullable: false},
			contain: []string{"IDENTITY(1,1)"},
		},
		{
			name:    "with default",
			col:     columnMeta{Name: "status", SystemTypeName: "varchar", MaxLength: 20, Nullable: true, DefaultDefinition: "'active'"},
			contain: []string{"DEFAULT 'active'"},
		},
		{
			name:    "computed column",
			col:     columnMeta{Name: "full_name", Computed: true, ComputedDefinition: "[first] + ' ' + [last]"},
			contain: []string{"[full_name]", "AS", "[first] + ' ' + [last]"},
		},
		{
			name:    "computed persisted",
			col:     columnMeta{Name: "total", Computed: true, ComputedPersisted: true, ComputedDefinition: "[qty] * [price]"},
			contain: []string{"PERSISTED"},
		},
		{
			name:    "sparse column",
			col:     columnMeta{Name: "notes", SystemTypeName: "nvarchar", MaxLength: -1, Nullable: true, Sparse: true},
			contain: []string{"SPARSE", "NULL"},
		},
		{
			name:    "rowguidcol",
			col:     columnMeta{Name: "uid", SystemTypeName: "uniqueidentifier", Nullable: false, RowGuidCol: true},
			contain: []string{"ROWGUIDCOL", "NOT NULL"},
		},
		{
			name:    "collation",
			col:     columnMeta{Name: "code", SystemTypeName: "varchar", MaxLength: 10, Nullable: true, Collation: "Latin1_General_CI_AS"},
			contain: []string{"COLLATE Latin1_General_CI_AS"},
		},
		{
			name:    "collation on non-char type ignored",
			col:     columnMeta{Name: "id", SystemTypeName: "int", Nullable: false, Collation: "Latin1_General_CI_AS"},
			contain: []string{"[id]", "int", "NOT NULL"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.col.DefinitionSQL()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, want := range test.contain {
				if !strings.Contains(got, want) {
					t.Fatalf("expected %q in %q", want, got)
				}
			}
		})
	}
}

func TestDefinitionSQLErrorUnsupportedType(t *testing.T) {
	col := columnMeta{Name: "bad", SystemTypeName: "unknown_type"}
	_, err := col.DefinitionSQL()
	if err == nil {
		t.Fatal("expected error for unsupported type in DefinitionSQL")
	}
}

func TestCreateTableSQL(t *testing.T) {
	table := tableMeta{
		Schema: "dbo",
		Name:   "users",
		Columns: []columnMeta{
			{Name: "id", SystemTypeName: "int", Identity: true, Nullable: false},
			{Name: "name", SystemTypeName: "nvarchar", MaxLength: 200, Nullable: false},
			{Name: "created_at", SystemTypeName: "datetime2", Scale: 3, Nullable: false, DefaultDefinition: "sysutcdatetime()"},
		},
	}
	got, err := table.CreateTableSQL()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wants := []string{
		"CREATE TABLE [dbo].[users]",
		"[id]",
		"IDENTITY(1,1)",
		"[name]",
		"nvarchar(100)",
		"[created_at]",
		"DEFAULT sysutcdatetime()",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in CREATE TABLE SQL:\n%s", want, got)
		}
	}
}

func TestCreateTableSQLEmptyColumns(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "empty_table"}
	_, err := table.CreateTableSQL()
	if err == nil {
		t.Fatal("expected error for table with no columns")
	}
}

func TestCreateViewSQL(t *testing.T) {
	view := viewMeta{
		Schema: "sales",
		Name:   "recent_orders",
		Definition: `CREATE VIEW [sales].[recent_orders] AS
SELECT [id], [created_at]
FROM [sales].[orders]
WHERE [created_at] >= '2026-01-01'`,
	}

	got := view.CreateViewSQL()
	if strings.Count(strings.ToUpper(got), "CREATE VIEW") != 0 {
		t.Fatalf("expected rewritten definition without nested CREATE VIEW, got:\n%s", got)
	}
	if !strings.HasPrefix(got, "CREATE OR ALTER VIEW [sales].[recent_orders] AS\n") {
		t.Fatalf("unexpected CREATE VIEW prefix:\n%s", got)
	}
	if !strings.Contains(got, "FROM [sales].[orders]") {
		t.Fatalf("expected view body to be preserved, got:\n%s", got)
	}
}

func TestCreateViewSQLPreservesWithClause(t *testing.T) {
	view := viewMeta{
		Schema: "dbo",
		Name:   "v1",
		Definition: `ALTER VIEW dbo.v1 WITH SCHEMABINDING AS
SELECT [id]
FROM dbo.t1`,
	}

	got := view.CreateViewSQL()
	if !strings.HasPrefix(got, "CREATE OR ALTER VIEW [dbo].[v1] WITH SCHEMABINDING AS\n") {
		t.Fatalf("expected WITH clause to be preserved, got:\n%s", got)
	}
}

func TestCreateViewSQLStripsBatchPreamble(t *testing.T) {
	view := viewMeta{
		Schema: "dbo",
		Name:   "v1",
		Definition: `SET ANSI_NULLS ON
GO
SET QUOTED_IDENTIFIER ON
GO
CREATE VIEW [dbo].[v1] AS
SELECT [id]
FROM [dbo].[t1]`,
	}

	got := view.CreateViewSQL()
	if !strings.HasPrefix(got, "CREATE OR ALTER VIEW [dbo].[v1] AS\n") {
		t.Fatalf("unexpected CREATE VIEW prefix:\n%s", got)
	}
	if strings.Contains(strings.ToUpper(got), "SET ANSI_NULLS") || strings.Contains(strings.ToUpper(got), "SET QUOTED_IDENTIFIER") {
		t.Fatalf("expected batch preamble to be removed, got:\n%s", got)
	}
	if strings.Contains(strings.ToUpper(got), "\nGO\n") {
		t.Fatalf("expected GO batch separators to be removed, got:\n%s", got)
	}
}

func TestCreateSequenceSQL(t *testing.T) {
	sequence := sequenceMeta{
		Schema:      "seq",
		Name:        "order_seq",
		TypeName:    "int",
		Precision:   10,
		Scale:       0,
		Increment:   "5",
		MinValue:    "100",
		MaxValue:    "1000",
		RestartWith: "110",
		IsCycling:   false,
		IsCached:    true,
		CacheSize:   10,
	}

	got, err := sequence.CreateSequenceSQL()
	if err != nil {
		t.Fatalf("CreateSequenceSQL() unexpected error: %v", err)
	}
	wants := []string{
		"IF OBJECT_ID(N'[seq].[order_seq]', 'SO') IS NULL",
		"CREATE SEQUENCE [seq].[order_seq] AS int START WITH 110 INCREMENT BY 5 MINVALUE 100 MAXVALUE 1000 NO CYCLE CACHE 10;",
		"ELSE",
		"ALTER SEQUENCE [seq].[order_seq] RESTART WITH 110 INCREMENT BY 5 MINVALUE 100 MAXVALUE 1000 NO CYCLE CACHE 10;",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in sequence SQL:\n%s", want, got)
		}
	}
}

func TestCreateProcedureSQL(t *testing.T) {
	procedure := procedureMeta{
		Schema: "app",
		Name:   "count_orders",
		Definition: `CREATE PROCEDURE [app].[count_orders] @minID int AS
BEGIN
    SELECT COUNT(*) AS [count]
    FROM [sales].[orders]
    WHERE [id] >= @minID
END`,
	}

	got := procedure.CreateProcedureSQL()
	if !strings.HasPrefix(got, "CREATE OR ALTER PROCEDURE [app].[count_orders] @minID int AS\n") {
		t.Fatalf("unexpected procedure prefix:\n%s", got)
	}
	if !strings.Contains(got, "FROM [sales].[orders]") {
		t.Fatalf("expected body to be preserved, got:\n%s", got)
	}
}

func TestCreateFunctionSQL(t *testing.T) {
	function := functionMeta{
		Schema: "app",
		Name:   "count_orders",
		Definition: `CREATE FUNCTION [app].[count_orders] (@minID int)
RETURNS int
AS
BEGIN
    RETURN (
        SELECT COUNT(*)
        FROM [sales].[orders]
        WHERE [id] >= @minID
    )
END`,
	}

	got := function.CreateFunctionSQL()
	if !strings.HasPrefix(got, "CREATE OR ALTER FUNCTION [app].[count_orders] (@minID int)\n") {
		t.Fatalf("unexpected function prefix:\n%s", got)
	}
	if !strings.Contains(got, "RETURNS int") || !strings.Contains(got, "FROM [sales].[orders]") {
		t.Fatalf("expected function body to be preserved, got:\n%s", got)
	}
}

func TestCreateTriggerSQL(t *testing.T) {
	trigger := triggerMeta{
		Schema: "sales",
		Name:   "trg_orders_audit",
		Definition: `CREATE TRIGGER [sales].[trg_orders_audit] ON [sales].[orders]
AFTER INSERT
AS
BEGIN
    INSERT INTO [sales].[order_audit] ([order_id])
    SELECT [id] FROM inserted
END`,
	}

	got := trigger.CreateTriggerSQL()
	if strings.Count(strings.ToUpper(got), "CREATE TRIGGER") != 0 {
		t.Fatalf("expected rewritten definition without nested CREATE TRIGGER, got:\n%s", got)
	}
	if !strings.HasPrefix(got, "CREATE OR ALTER TRIGGER [sales].[trg_orders_audit] ON [sales].[orders]\n") {
		t.Fatalf("unexpected trigger prefix:\n%s", got)
	}
	if !strings.Contains(got, "AFTER INSERT") || !strings.Contains(got, "INSERT INTO [sales].[order_audit]") {
		t.Fatalf("expected trigger body to be preserved, got:\n%s", got)
	}
}

func TestCreateSynonymSQL(t *testing.T) {
	synonym := synonymMeta{
		Schema:         "app",
		Name:           "orders_alias",
		BaseObjectName: "[sales].[orders]",
	}

	got := synonym.CreateSynonymSQL()
	wants := []string{
		"IF OBJECT_ID(N'[app].[orders_alias]', 'SN') IS NOT NULL DROP SYNONYM [app].[orders_alias];",
		"CREATE SYNONYM [app].[orders_alias] FOR [sales].[orders];",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in synonym SQL:\n%s", want, got)
		}
	}
}

func TestCreateTypeSQL(t *testing.T) {
	aliasType := aliasTypeMeta{
		Schema:         "sales",
		Name:           "order_code",
		SystemTypeName: "nvarchar",
		MaxLength:      24,
		Nullable:       true,
	}

	got, err := aliasType.CreateTypeSQL()
	if err != nil {
		t.Fatalf("CreateTypeSQL() unexpected error: %v", err)
	}
	wants := []string{
		"IF TYPE_ID(N'[sales].[order_code]') IS NULL",
		"CREATE TYPE [sales].[order_code] FROM nvarchar(12) NULL;",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in type SQL:\n%s", want, got)
		}
	}
}

func TestCreateTableTypeSQL(t *testing.T) {
	tableType := tableTypeMeta{
		Schema: "sales",
		Name:   "order_line_type",
		Columns: []columnMeta{
			{Name: "order_id", SystemTypeName: "int", Nullable: false},
			{Name: "order_code", SystemTypeName: "nvarchar", MaxLength: 24, Nullable: true},
		},
		PrimaryKey: &keyConstraint{
			Name:    "PK_order_line_type",
			Cluster: "NONCLUSTERED",
			Columns: []keyColumn{{Name: "order_id"}},
		},
		Checks: []checkConstraint{{Name: "CK_order_code", Definition: "([order_code] IS NULL OR len([order_code]) > 0)"}},
	}

	got, err := tableType.CreateTypeSQL()
	if err != nil {
		t.Fatalf("CreateTypeSQL() unexpected error: %v", err)
	}
	wants := []string{
		"IF TYPE_ID(N'[sales].[order_line_type]') IS NULL",
		"CREATE TYPE [sales].[order_line_type] AS TABLE (",
		"[order_id] int NOT NULL",
		"[order_code] nvarchar(12) NULL",
		"PRIMARY KEY NONCLUSTERED ([order_id] ASC)",
		"CHECK ([order_code] IS NULL OR len([order_code]) > 0)",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in table type SQL:\n%s", want, got)
		}
	}
}

func TestTypeDeclarationUserDefined(t *testing.T) {
	col := columnMeta{TypeSchema: "sales", UserTypeName: "order_code", IsUserDefined: true}
	got, err := col.TypeDeclaration()
	if err != nil {
		t.Fatalf("TypeDeclaration() unexpected error: %v", err)
	}
	if got != "[sales].[order_code]" {
		t.Fatalf("TypeDeclaration() = %q, want %q", got, "[sales].[order_code]")
	}
}

func TestPrimaryKeySQL(t *testing.T) {
	table := tableMeta{
		Schema: "dbo",
		Name:   "orders",
		PrimaryKey: &keyConstraint{
			Name:    "PK_orders",
			Cluster: "CLUSTERED",
			Columns: []keyColumn{
				{Name: "id", Desc: false},
			},
		},
	}
	got := table.PrimaryKeySQL()
	wants := []string{
		"ALTER TABLE [dbo].[orders]",
		"ADD CONSTRAINT [PK_orders]",
		"PRIMARY KEY CLUSTERED",
		"([id] ASC)",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}

func TestPrimaryKeySQLNonClustered(t *testing.T) {
	table := tableMeta{
		Schema: "dbo",
		Name:   "logs",
		PrimaryKey: &keyConstraint{
			Name:    "PK_logs",
			Cluster: "NONCLUSTERED",
			Columns: []keyColumn{
				{Name: "log_id", Desc: true},
			},
		},
	}
	got := table.PrimaryKeySQL()
	if !strings.Contains(got, "PRIMARY KEY NONCLUSTERED") {
		t.Fatalf("expected NONCLUSTERED in %q", got)
	}
	if !strings.Contains(got, "[log_id] DESC") {
		t.Fatalf("expected DESC in %q", got)
	}
}

func TestPrimaryKeySQLDefaultCluster(t *testing.T) {
	table := tableMeta{
		Schema: "dbo",
		Name:   "items",
		PrimaryKey: &keyConstraint{
			Name:    "PK_items",
			Cluster: "",
			Columns: []keyColumn{{Name: "id"}},
		},
	}
	got := table.PrimaryKeySQL()
	if !strings.Contains(got, "PRIMARY KEY CLUSTERED") {
		t.Fatalf("expected default CLUSTERED in %q", got)
	}
}

func TestCheckSQL(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "products"}
	check := checkConstraint{Name: "CK_price", Definition: "([price]>(0))", Trusted: true}
	got := table.CheckSQL(check)
	wants := []string{
		"ALTER TABLE [dbo].[products]",
		"WITH CHECK",
		"ADD CONSTRAINT [CK_price]",
		"CHECK ([price]>(0))",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}

func TestCheckSQLNotTrusted(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "products"}
	check := checkConstraint{Name: "CK_price", Definition: "([price]>(0))", Trusted: false}
	got := table.CheckSQL(check)
	if !strings.Contains(got, "WITH NOCHECK") {
		t.Fatalf("expected WITH NOCHECK in %q", got)
	}
}

func TestForeignKeySQL(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "order_items"}
	fk := foreignKey{
		Name:         "FK_order_items_orders",
		Columns:      []string{"order_id"},
		RefSchema:    "dbo",
		RefTable:     "orders",
		RefColumns:   []string{"id"},
		DeleteAction: "CASCADE",
		UpdateAction: "SET_NULL",
		Trusted:      true,
	}
	got := table.ForeignKeySQL(fk)
	wants := []string{
		"ALTER TABLE [dbo].[order_items]",
		"WITH CHECK",
		"ADD CONSTRAINT [FK_order_items_orders]",
		"FOREIGN KEY ([order_id])",
		"REFERENCES [dbo].[orders] ([id])",
		"ON DELETE CASCADE",
		"ON UPDATE SET NULL",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}

func TestForeignKeySQLNotTrusted(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "order_items"}
	fk := foreignKey{
		Name:       "FK_order_items_orders",
		Columns:    []string{"order_id"},
		RefSchema:  "dbo",
		RefTable:   "orders",
		RefColumns: []string{"id"},
		Trusted:    false,
	}
	got := table.ForeignKeySQL(fk)
	if !strings.Contains(got, "WITH NOCHECK") {
		t.Fatalf("expected WITH NOCHECK in %q", got)
	}
}

func TestForeignKeySQLNoActions(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "order_items"}
	fk := foreignKey{
		Name:         "FK_order_items_orders",
		Columns:      []string{"order_id"},
		RefSchema:    "dbo",
		RefTable:     "orders",
		RefColumns:   []string{"id"},
		DeleteAction: "NO_ACTION",
		UpdateAction: "",
		Trusted:      true,
	}
	got := table.ForeignKeySQL(fk)
	if strings.Contains(got, "ON DELETE") {
		t.Fatalf("expected no ON DELETE clause in %q", got)
	}
	if strings.Contains(got, "ON UPDATE") {
		t.Fatalf("expected no ON UPDATE clause in %q", got)
	}
}

func TestForeignKeySQLMultiColumn(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "composite_fk"}
	fk := foreignKey{
		Name:       "FK_composite",
		Columns:    []string{"col_a", "col_b"},
		RefSchema:  "ref_schema",
		RefTable:   "ref_table",
		RefColumns: []string{"ref_a", "ref_b"},
		Trusted:    true,
	}
	got := table.ForeignKeySQL(fk)
	if !strings.Contains(got, "FOREIGN KEY ([col_a], [col_b])") {
		t.Fatalf("expected composite FK columns in %q", got)
	}
	if !strings.Contains(got, "REFERENCES [ref_schema].[ref_table] ([ref_a], [ref_b])") {
		t.Fatalf("expected composite ref columns in %q", got)
	}
}

func TestIndexSQL(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "users"}
	index := indexMeta{
		Name:    "IX_users_email",
		Unique:  true,
		Cluster: "NONCLUSTERED",
		KeyColumns: []keyColumn{
			{Name: "email", Desc: false},
		},
	}
	got := table.IndexSQL(index)
	wants := []string{
		"CREATE UNIQUE NONCLUSTERED INDEX [IX_users_email] ON [dbo].[users]",
		"([email] ASC)",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}

func TestIndexSQLNonUnique(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "logs"}
	index := indexMeta{
		Name:       "IX_logs_timestamp",
		Unique:     false,
		Cluster:    "CLUSTERED",
		KeyColumns: []keyColumn{{Name: "created_at", Desc: true}},
	}
	got := table.IndexSQL(index)
	if !strings.Contains(got, "CREATE CLUSTERED INDEX") {
		t.Fatalf("expected CLUSTERED INDEX in %q", got)
	}
	if !strings.Contains(got, "[created_at] DESC") {
		t.Fatalf("expected DESC in %q", got)
	}
}

func TestIndexSQLDefaultCluster(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "items"}
	index := indexMeta{
		Name:       "IX_items_name",
		Cluster:    "",
		KeyColumns: []keyColumn{{Name: "name"}},
	}
	got := table.IndexSQL(index)
	if !strings.Contains(got, "NONCLUSTERED INDEX") {
		t.Fatalf("expected default NONCLUSTERED in %q", got)
	}
}

func TestIndexSQLWithInclude(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "orders"}
	index := indexMeta{
		Name:       "IX_orders_cover",
		Cluster:    "NONCLUSTERED",
		KeyColumns: []keyColumn{{Name: "customer_id"}},
		Include:    []string{"order_date", "total"},
	}
	got := table.IndexSQL(index)
	if !strings.Contains(got, "INCLUDE ([order_date], [total])") {
		t.Fatalf("expected INCLUDE clause in %q", got)
	}
}

func TestIndexSQLWithFilter(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "orders"}
	index := indexMeta{
		Name:       "IX_orders_active",
		Cluster:    "NONCLUSTERED",
		KeyColumns: []keyColumn{{Name: "status"}},
		Filter:     "([status] = 'active')",
	}
	got := table.IndexSQL(index)
	if !strings.Contains(got, "WHERE ([status] = 'active')") {
		t.Fatalf("expected WHERE filter in %q", got)
	}
}

func TestIndexSQLWithIncludeAndFilter(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "orders"}
	index := indexMeta{
		Name:       "IX_orders_filtered_cover",
		Cluster:    "NONCLUSTERED",
		KeyColumns: []keyColumn{{Name: "region"}},
		Include:    []string{"amount"},
		Filter:     "([region] IS NOT NULL)",
	}
	got := table.IndexSQL(index)
	if !strings.Contains(got, "INCLUDE ([amount])") {
		t.Fatalf("expected INCLUDE in %q", got)
	}
	if !strings.Contains(got, "WHERE ([region] IS NOT NULL)") {
		t.Fatalf("expected WHERE filter in %q", got)
	}
}
