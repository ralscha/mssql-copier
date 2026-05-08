# mssql-copier

A fast, concurrent SQL Server copier that replicates SQL Server tables, alias user-defined types, user-defined table types, sequences, views, functions, stored procedures, DML triggers, and synonyms from a source database to a target database. It uses bulk copy where possible, with automatic fallback to row-by-row inserts for unsupported column types.

## Features

- **Metadata-driven** — discovers tables, alias user-defined types, user-defined table types, sequences, views, functions, stored procedures, DML triggers, synonyms, columns, primary keys, foreign keys, checks, and indexes from the source
- **Concurrent copying** — copies multiple tables in parallel with configurable worker count
- **Bulk copy** — uses SQL Server's `COPY IN` (TABLOCK) for compatible column types, falling back to row-by-row `INSERT` when needed
- **Identity insert** — automatically handles `SET IDENTITY_INSERT ON/OFF`
- **Object filters** — include/exclude schemas and object names using wildcard patterns (`*`, `%`, `?`, `_`)
- **Sequence copy** — copies sequences so target-side defaults based on `NEXT VALUE FOR ...` keep working
- **Alias type copy** — copies alias user-defined types and preserves them in recreated table definitions
- **Table type copy** — copies user-defined table types so TVP-based procedures can be recreated on the target
- **Trigger copy** — copies table-scoped DML triggers with rerun-safe `CREATE OR ALTER TRIGGER`
- **View copy** — copies views with dependency-aware creation order and rerun-safe `CREATE OR ALTER VIEW`
- **Function copy** — copies SQL functions with dependency-aware creation order and rerun-safe `CREATE OR ALTER FUNCTION`
- **Stored procedure copy** — copies stored procedures with rerun-safe `CREATE OR ALTER PROCEDURE`
- **Synonym copy** — copies synonyms with rerun-safe drop-and-create behavior
- **Plan mode** — preview the execution plan without modifying the target
- **Liquibase export mode** — writes an initial Liquibase formatted SQL file for the discovered schema objects
- **Drop-existing mode** — optionally drop matching target tables before recreating them
- **Fake data replacement** — replace configured column values during copy and data export using `gofakeit`
- **Post-data objects** — creates primary keys, checks, foreign keys, and indexes after data is loaded
- **Integration tested** — includes testcontainers-based integration tests

## Installation

```sh
go install ./cmd/mssql-copier
```

Or build from source:

```sh
go build -o mssql-copier ./cmd/mssql-copier
```

## Project layout

```text
cmd/mssql-copier/   CLI entrypoint
internal/copier/    copier engine, SQL metadata logic, and tests
```

## Usage

### Basic copy

```sh
mssql-copier \
  --source "sqlserver://user:pass@source-host:1433?database=SourceDB" \
  --target "sqlserver://user:pass@target-host:1433?database=TargetDB"
```

When the target host is not local (`localhost`, `127.0.0.1`, or loopback IPv6 such as `::1`), the CLI asks for an explicit `yes` before it opens the target connection.

### Plan mode (dry run)

Preview which objects would be copied without touching the target. In plan mode, only `--source` is required; `--target` is optional and is shown in the output only when provided:

```sh
mssql-copier --plan --source "sqlserver://..."
```

### DDL export

Write a source-only DDL baseline file for the selected schema objects. The generated file is Liquibase-formatted SQL, so it can be used directly as a Liquibase changelog. This mode exports DDL only; it does not export table data.

```sh
mssql-copier \
  --source "sqlserver://..." \
  --export-ddl ./export/initial.sql
```

The generated file contains ordered Liquibase changesets for schemas, types, sequences, tables, constraints, indexes, views, functions, synonyms, procedures, and triggers. Because this is an initial baseline export, `--drop-existing` is not supported with this mode.

### Data export

Write a source-only data seed file for the selected tables. The generated file is plain SQL with semicolon-terminated `SET IDENTITY_INSERT` and `INSERT` statements, with no `GO` batches.

```sh
mssql-copier \
  --source "sqlserver://..." \
  --export-data ./export/initial-data.sql
```

The generated file contains deterministic table sections and row inserts ordered by primary key when available. It temporarily disables constraints on the exported tables before loading rows and re-checks them at the end so the script can run cleanly after a schema import even when foreign keys already exist. This mode exports table data only; it does not create schema objects, and `--drop-existing` is not supported with this mode.

For integration testing, you can cap the export to the first `N` rows per selected table:

```sh
mssql-copier \
  --source "sqlserver://..." \
  --export-data ./export/test-seed.sql \
  --export-data-rows 25
```

The row cap starts from the deterministic per-table sample and then pulls in any referenced parent rows needed to keep copied foreign keys valid inside the exported set.

### Filtering objects

Filters are applied by schema name and object name across copied tables and other discovered objects.

When a filtered copy excludes a referenced parent table and that table is not already present on the target, foreign key recreation for the copied child table is skipped.

```sh
# Copy only tables in the "sales" schema
mssql-copier --source "..." --target "..." --include-schemas "sales"

# Exclude audit tables
mssql-copier --source "..." --target "..." --exclude-tables "*.audit_%"

# Copy only specific tables (schema-qualified)
mssql-copier --source "..." --target "..." --include-tables "dbo.orders,sales.customers"
```

### Drop and recreate

```sh
mssql-copier --source "..." --target "..." --drop-existing
```

Alias user-defined types and user-defined table types are created before tables and procedures so recreated table definitions can keep alias types and TVP-based procedures can compile on the target. Sequences are created before tables so defaults like `NEXT VALUE FOR ...` work during table creation. Views are created after tables and indexes. Functions are created after views in dependency order. Synonyms are then recreated so late-bound references are available to programmable objects. Stored procedures are created after tables, views, functions, table types, and synonyms, with explicit dependency ordering across copied procedures. Table-scoped DML triggers are created after tables, procedures, and synonyms so they bind to recreated target tables without firing during the initial data copy and can reference copied synonyms. Existing target sequences, views, functions, procedures, triggers, and synonyms are refreshed on reruns. Alias types are recreated on rerun only when `--drop-existing` is set. Table types are created only when missing.

### Tuning

```sh
mssql-copier \
  --source "..." --target "..." \
  --workers 8 \
  --batch-size 10000
```

### YAML configuration

You can keep most parameters in a YAML file. By default, the copier looks for `mssql-copier.yml` in the current working directory.

A starter template is checked in at `mssql-copier.example.yml`.

```sh
# Uses ./mssql-copier.yml when present
mssql-copier

# Use a custom config file path
mssql-copier --config ./config/prod.yml
```

CLI flags override values from YAML when both are provided.

`--export-ddl` and `--export-data` must be passed as CLI flags. This lets export modes still reuse YAML values such as `source`, `workers`, and include/exclude filters.

`--export-data-rows` is also CLI-only and only applies together with `--export-data`.

`fake-data` is YAML-only. Each entry maps a column selector to a [`gofakeit`](https://github.com/brianvoe/gofakeit) function. Selectors support:

- exact column name: `name`
- exact table and column: `users.name`
- exact schema, table, and column: `dbo.users.name`
- regex: any selector that is not a plain identifier path is treated as a case-insensitive regex and matched against `column`, `table.column`, and `schema.table.column`. Example: `name.*`

Use `gofakeit` function names like `Email`, `FirstName`, or `LoremIpsumSentence`.

Parameters are optional and are appended after the function name using `;` in declared order. Examples: `LoremIpsumSentence;10` and `Price;1;100`.

Example `mssql-copier.yml`:

```yaml
source: sqlserver://user:pass@source-host:1433?database=SourceDB
target: sqlserver://user:pass@target-host:1433?database=TargetDB
workers: 8
batch-size: 10000
verbose: true
drop-existing: false
include-schemas:
  - sales
  - reporting
exclude-schemas:
  - audit
include-tables:
  - sales.orders
  - sales.customers
exclude-tables:
  - "*.audit_%"
fake-data:
  users.name: Name
  email: Email
  name.*: FirstName
  summary: LoremIpsumSentence;10
  amount: Price;1;100
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `mssql-copier.yml` | Path to YAML config file; optional when using the default path |
| `--source` | *(required)* | Source SQL Server DSN |
| `--target` | *(required unless `--plan`)* | Target SQL Server DSN; non-local targets require an interactive `yes` confirmation |
| `--plan` | `false` | Print execution plan without modifying target |
| `--export-ddl` | | Write Liquibase-formatted DDL to a file; `--target` is not required |
| `--export-data` | | Write plain SQL data inserts to a file; `--target` is not required |
| `--export-data-rows` | `0` | Limit `--export-data` to the first N rows per selected table |
| `--workers` | `max(2, NumCPU())` | Number of concurrent table copy workers |
| `--batch-size` | `5000` | Rows per bulk batch hint |
| `--drop-existing` | `false` | Drop matching target tables before recreating |
| `--verbose` | `true` | Log per-table activity |
| `--include-schemas` | | Comma-separated schema names or wildcard patterns (YAML: list) |
| `--exclude-schemas` | | Comma-separated schema names or wildcard patterns (YAML: list) |
| `--include-tables` | | Comma-separated table names (`name` or `schema.name`) or wildcard patterns (YAML: list) |
| `--exclude-tables` | | Comma-separated table names or wildcard patterns (YAML: list) |

### Fake data replacement

Configured fake-data rules are applied in both copy mode and `--export-data` mode before values are written to the target or serialized into SQL inserts.

Rule precedence is:

1. exact `schema.table.column`
2. exact `table.column`
3. exact `column`
4. regex selectors, matched in deterministic order

Examples:

```yaml
fake-data:
  customer.email: Email
  ssn: SSN
  dbo.people.name: Name
  name.*: FirstName
  description: LoremIpsumSentence;10
  price: Price;1;100
```

The CLI validates every configured function and parameter list at startup and fails fast when a function name is unknown, parameters do not fit the selected function, or the function returns a complex value type that the copier cannot safely write to SQL Server.

### DSN format

Uses the `go-mssqldb` driver. Examples:

```
sqlserver://user:password@host:1433?database=MyDB
sqlserver://user:password@host:1433?database=MyDB&encrypt=true&trustservercertificate=true
```

### Wildcard patterns

Filter arguments support SQL-style and glob-style wildcards:
- `*` or `%` — matches any sequence of characters
- `?` or `_` — matches exactly one character

Examples: `sales*`, `dbo.%`, `audit_202?`, `*_archive`

## How it works

1. **Discover** — queries `sys.tables`, `sys.types`, `sys.table_types`, `sys.sequences`, `sys.views`, `sys.objects`, `sys.procedures`, `sys.triggers`, `sys.synonyms`, `sys.columns`, `sys.indexes`, and other system catalog views on the source to build metadata for copied objects
2. **Filter** — applies include/exclude rules against schema names and object names
3. **Plan** (optional) — prints the planned actions and exits
4. **Create schemas** — creates any missing non-`dbo` schemas needed by copied objects on the target
5. **Prepare target** — optionally drops existing target tables
6. **Create alias types** — creates copied alias user-defined types before tables are recreated
7. **Create table types** — creates copied user-defined table types before dependent procedures are recreated
8. **Create sequences** — creates or updates copied sequences before tables are created
9. **Create tables** — generates and executes `CREATE TABLE` statements from source column definitions (including defaults, computed columns, collations, and preserved alias types)
10. **Copy data** — distributes tables across worker goroutines; each table is copied in a single transaction using bulk copy or row-insert depending on column type compatibility
11. **Post-data objects** — creates primary keys, check constraints, foreign keys, and indexes
12. **Create views** — creates or updates copied views in dependency order
13. **Create functions** — creates or updates copied SQL functions in dependency order
14. **Create synonyms** — recreates copied synonyms after referenced objects are in place
15. **Create procedures** — creates or updates copied stored procedures after their copied dependencies are in place
16. **Create triggers** — creates or updates copied table-scoped DML triggers after tables, procedures, and synonyms are in place

### Bulk vs. row-insert

The copier prefers bulk copy (`COPY IN` with `TABLOCK`) for performance. It falls back to row-by-row `INSERT` statements when a table contains column types not supported by the bulk protocol (e.g., `xml`, `sql_variant`, user-defined types, etc.).

### Identity columns

Tables with identity columns have `SET IDENTITY_INSERT ON` enabled during the copy so source identity values are preserved.

### Views

Views are copied automatically with table copy. The copier reads the source view definitions, keeps inter-view dependencies in order, and applies them to the target with `CREATE OR ALTER VIEW` so repeated runs stay idempotent.

### Functions

SQL scalar and table-valued functions are copied automatically with table copy. The copier reads their definitions with `OBJECT_DEFINITION`, orders copied functions by inter-function dependencies, and applies them with `CREATE OR ALTER FUNCTION` so reruns stay idempotent.

### Sequences

Sequences are copied automatically with table copy. Their definitions are created on the target when missing and altered on rerun so defaults based on `NEXT VALUE FOR ...` continue to work after the table data is copied.

### Alias User-Defined Types

Alias user-defined types are copied automatically with table copy. For alias types based on built-in SQL Server scalar types, the copier preserves the alias type in recreated table column definitions. Existing target alias types are recreated automatically only when `--drop-existing` is set.

### User-Defined Table Types

User-defined table types are copied automatically with procedure copy. The copier creates the table type object when it is missing so stored procedures that use table-valued parameters can be recreated on the target.

SQL Server does not support `CREATE OR ALTER TYPE`, so existing table type definitions are not rewritten on rerun. If a copied table type changes shape on the source, drop the target type and rerun the copier.

### Stored Procedures

Stored procedures are copied automatically with table copy. Their definitions are applied with `CREATE OR ALTER PROCEDURE`, and copied procedures are ordered using dependencies from `sys.sql_expression_dependencies` so procedures that reference other copied procedures or synonyms are created after those dependencies.

### Triggers

Table-scoped DML triggers are copied automatically with table copy. The copier reads trigger definitions from `OBJECT_DEFINITION`, resolves dependencies on copied programmable objects from `sys.sql_expression_dependencies`, recreates them with `CREATE OR ALTER TRIGGER`, and reapplies the enabled or disabled state from the source.

### Synonyms

Synonyms are copied automatically with table copy. Because SQL Server does not support `CREATE OR ALTER SYNONYM`, the copier drops an existing target synonym and recreates it from `sys.synonyms.base_object_name` on each run.

## Development

This project uses [Task](https://taskfile.dev) for build automation.

```sh
# Show available tasks
task

# Format code
task format

# Build
task build

# Run unit tests
task test

# Run integration tests (requires Docker)
task test:integration
```

Integration tests use [testcontainers-go](https://github.com/testcontainers/testcontainers-go) to spin up real SQL Server instances in Docker.

## License

MIT License. See [LICENSE](LICENSE) for details.
