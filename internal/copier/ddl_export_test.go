package copier

import (
	"strings"
	"testing"
)

func TestBuildFlywayBaselineSQL(t *testing.T) {
	c := &copier{
		cfg: config{ExportDDLFile: "baseline.sql"},
		aliasTypes: []aliasTypeMeta{{
			Schema:         "sales",
			Name:           "order_code",
			SystemTypeName: "nvarchar",
			MaxLength:      24,
			Nullable:       false,
		}},
		sequences: []sequenceMeta{{
			Schema:      "sales",
			Name:        "order_seq",
			TypeName:    "int",
			Increment:   "1",
			MinValue:    "1",
			MaxValue:    "2147483647",
			RestartWith: "5",
		}},
		tables: []tableMeta{
			{
				Schema: "sales",
				Name:   "customers",
				Columns: []columnMeta{
					{Name: "id", SystemTypeName: "int", Nullable: false},
				},
				PrimaryKey: &keyConstraint{Name: "PK_customers", Columns: []keyColumn{{Name: "id"}}},
			},
			{
				Schema: "sales",
				Name:   "orders",
				Columns: []columnMeta{
					{Name: "id", SystemTypeName: "int", Nullable: false, DefaultDefinition: "NEXT VALUE FOR [sales].[order_seq]"},
					{Name: "customer_id", SystemTypeName: "int", Nullable: false},
				},
				PrimaryKey: &keyConstraint{Name: "PK_orders", Columns: []keyColumn{{Name: "id"}}},
				Checks:     []checkConstraint{{Name: "CK_orders_id", Definition: "([id] > 0)", Disabled: true}},
				ForeignKeys: []foreignKey{
					{Name: "FK_orders_customers", Columns: []string{"customer_id"}, RefSchema: "sales", RefTable: "customers", RefColumns: []string{"id"}, Trusted: true},
					{Name: "FK_orders_external", Columns: []string{"customer_id"}, RefSchema: "ext", RefTable: "customers", RefColumns: []string{"id"}},
				},
			},
		},
		views: []viewMeta{{
			Schema:     "sales",
			Name:       "v_orders",
			Definition: "CREATE VIEW [sales].[v_orders] AS SELECT [id] FROM [sales].[orders]",
		}},
		functions: []functionMeta{{
			Schema:     "sales",
			Name:       "fn_order_count",
			Definition: "CREATE FUNCTION [sales].[fn_order_count] () RETURNS int AS BEGIN RETURN (SELECT COUNT(*) FROM [sales].[orders]) END",
		}},
		procedures: []procedureMeta{{
			Schema:     "sales",
			Name:       "p_refresh_orders",
			Definition: "CREATE PROCEDURE [sales].[p_refresh_orders] AS BEGIN SELECT 1 END",
		}},
		triggers: []triggerMeta{{
			Schema:      "sales",
			Name:        "trg_orders_audit",
			TableSchema: "sales",
			TableName:   "orders",
			Definition:  "CREATE TRIGGER [sales].[trg_orders_audit] ON [sales].[orders] AFTER INSERT AS BEGIN SELECT 1 END",
			Disabled:    true,
		}},
		synonyms: []synonymMeta{{
			Schema:         "sales",
			Name:           "orders_alias",
			BaseObjectName: "[sales].[orders]",
		}},
	}

	got, err := c.buildFlywayBaselineSQL()
	if err != nil {
		t.Fatalf("buildFlywayBaselineSQL() unexpected error: %v", err)
	}

	wants := []string{
		"IF SCHEMA_ID(N'sales') IS NULL EXEC(N'CREATE SCHEMA [sales]')",
		"CREATE TYPE [sales].[order_code] FROM nvarchar(12) NOT NULL;",
		"CREATE TABLE [sales].[customers]",
		"CREATE TABLE [sales].[orders]",
		"ALTER TABLE [sales].[orders] WITH CHECK ADD CONSTRAINT [FK_orders_customers] FOREIGN KEY ([customer_id]) REFERENCES [sales].[customers] ([id]);",
		"ALTER TABLE [sales].[orders] NOCHECK CONSTRAINT [CK_orders_id]",
		"CREATE OR ALTER VIEW [sales].[v_orders] AS SELECT [id] FROM [sales].[orders]",
		"CREATE OR ALTER FUNCTION [sales].[fn_order_count] () RETURNS int AS BEGIN RETURN (SELECT COUNT(*) FROM [sales].[orders]) END",
		"CREATE SYNONYM [sales].[orders_alias] FOR [sales].[orders];",
		"CREATE OR ALTER PROCEDURE [sales].[p_refresh_orders] AS BEGIN SELECT 1 END",
		"CREATE OR ALTER TRIGGER [sales].[trg_orders_audit] ON [sales].[orders] AFTER INSERT AS BEGIN SELECT 1 END\nGO\nDISABLE TRIGGER",
		"DISABLE TRIGGER [sales].[trg_orders_audit] ON [sales].[orders];",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in Flyway SQL:\n%s", want, got)
		}
	}

	if !strings.HasPrefix(got, "-- mssql-copier Flyway SQL Server baseline\n") {
		t.Fatalf("expected Flyway header in DDL export, got:\n%s", got)
	}
	if strings.Contains(got, "--changeset ") || strings.Contains(got, "splitStatements") {
		t.Fatalf("expected Flyway SQL rather than Liquibase changesets, got:\n%s", got)
	}
	if !strings.Contains(got, "\nGO\n") {
		t.Fatalf("expected SQL Server GO batch delimiters, got:\n%s", got)
	}

	if strings.Contains(got, "FK_orders_external") {
		t.Fatalf("expected foreign key to excluded table to be omitted, got:\n%s", got)
	}

	assertOrder := func(first string, second string) {
		t.Helper()
		firstIdx := strings.Index(got, first)
		secondIdx := strings.Index(got, second)
		if firstIdx == -1 || secondIdx == -1 {
			t.Fatalf("missing order markers %q or %q", first, second)
		}
		if firstIdx >= secondIdx {
			t.Fatalf("expected %q before %q in Flyway SQL", first, second)
		}
	}

	assertOrder("CREATE TYPE [sales].[order_code]", "CREATE TABLE [sales].[customers]")
	assertOrder("CREATE TABLE [sales].[orders]", "CREATE OR ALTER VIEW [sales].[v_orders]")
	assertOrder("CREATE OR ALTER VIEW [sales].[v_orders]", "CREATE OR ALTER FUNCTION [sales].[fn_order_count]")
	assertOrder("CREATE OR ALTER FUNCTION [sales].[fn_order_count]", "CREATE SYNONYM [sales].[orders_alias]")
	assertOrder("CREATE SYNONYM [sales].[orders_alias]", "CREATE OR ALTER PROCEDURE [sales].[p_refresh_orders]")
	assertOrder("CREATE OR ALTER PROCEDURE [sales].[p_refresh_orders]", "ALTER TABLE [sales].[orders] ADD CONSTRAINT [PK_orders]")
	assertOrder("ALTER TABLE [sales].[customers] ADD CONSTRAINT [PK_customers]", "ALTER TABLE [sales].[orders] WITH CHECK ADD CONSTRAINT [FK_orders_customers]")
	assertOrder("ALTER TABLE [sales].[orders] ADD CONSTRAINT [PK_orders]", "ALTER TABLE [sales].[orders] WITH CHECK ADD CONSTRAINT [FK_orders_customers]")
	assertOrder("ALTER TABLE [sales].[orders] WITH CHECK ADD CONSTRAINT [FK_orders_customers]", "CREATE OR ALTER TRIGGER [sales].[trg_orders_audit]")
}

func TestBuildFlywayBaselineSQLForeignKeyAfterAllPrimaryKeys(t *testing.T) {
	// Regression test: FK from orders→customers must come after customers' PK even when
	// orders appears first in c.tables (i.e. higher row count or different sort order).
	c := &copier{
		cfg: config{ExportDDLFile: "baseline.sql"},
		tables: []tableMeta{
			{
				Schema: "dbo",
				Name:   "orders",
				Columns: []columnMeta{
					{Name: "id", SystemTypeName: "int", Nullable: false},
					{Name: "customer_id", SystemTypeName: "int", Nullable: false},
				},
				PrimaryKey: &keyConstraint{Name: "PK_orders", Columns: []keyColumn{{Name: "id"}}},
				ForeignKeys: []foreignKey{
					{Name: "FK_orders_customers", Columns: []string{"customer_id"}, RefSchema: "dbo", RefTable: "customers", RefColumns: []string{"id"}, Trusted: true},
				},
			},
			{
				Schema: "dbo",
				Name:   "customers",
				Columns: []columnMeta{
					{Name: "id", SystemTypeName: "int", Nullable: false},
				},
				PrimaryKey: &keyConstraint{Name: "PK_customers", Columns: []keyColumn{{Name: "id"}}},
			},
		},
	}

	got, err := c.buildFlywayBaselineSQL()
	if err != nil {
		t.Fatalf("buildFlywayBaselineSQL() unexpected error: %v", err)
	}

	pkCustomers := strings.Index(got, "ADD CONSTRAINT [PK_customers]")
	pkOrders := strings.Index(got, "ADD CONSTRAINT [PK_orders]")
	fkOrders := strings.Index(got, "ADD CONSTRAINT [FK_orders_customers]")

	if pkCustomers == -1 || pkOrders == -1 || fkOrders == -1 {
		t.Fatalf("expected PK_customers, PK_orders and FK_orders_customers in output:\n%s", got)
	}
	if pkCustomers >= fkOrders {
		t.Errorf("PK_customers (pos %d) must appear before FK_orders_customers (pos %d)", pkCustomers, fkOrders)
	}
	if pkOrders >= fkOrders {
		t.Errorf("PK_orders (pos %d) must appear before FK_orders_customers (pos %d)", pkOrders, fkOrders)
	}
}
