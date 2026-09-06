# mssql-copier

A fast, concurrent SQL Server copier that replicates SQL Server tables, alias user-defined types, user-defined table types, sequences, views, functions, stored procedures, DML triggers, and synonyms from a source database to a target database. It uses bulk copy where possible, with automatic fallback to row-by-row inserts for unsupported column types.

## Features

- **Metadata-driven** - discovers tables, alias user-defined types, user-defined table types, sequences, views, functions, stored procedures, DML triggers, synonyms, columns, primary keys, foreign keys, checks, and indexes from the source
- **Concurrent copying** - copies multiple tables in parallel with configurable worker count
- **Bulk copy** - uses SQL Server's `COPY IN` (TABLOCK) for compatible column types, falling back to row-by-row `INSERT` when needed
- **Identity insert** - automatically handles `SET IDENTITY_INSERT ON/OFF`
- **Object filters** - include/exclude schemas and object names using wildcard patterns (`*`, `%`, `?`, `_`)
- **Sequence copy** - copies sequences so target-side defaults based on `NEXT VALUE FOR ...` keep working
- **Alias type copy** - copies alias user-defined types and preserves them in recreated table definitions
- **Table type copy** - copies user-defined table types so TVP-based procedures can be recreated on the target
- **Trigger copy** - copies table-scoped DML triggers with rerun-safe `CREATE OR ALTER TRIGGER`
- **View copy** - copies views with dependency-aware creation order and rerun-safe `CREATE OR ALTER VIEW`
- **Function copy** - copies SQL functions with dependency-aware creation order and rerun-safe `CREATE OR ALTER FUNCTION`
- **Stored procedure copy** - copies stored procedures with rerun-safe `CREATE OR ALTER PROCEDURE`
- **Synonym copy** - copies synonyms with rerun-safe drop-and-create behavior
- **Plan mode** - preview the execution plan without modifying the target
- **DDL export mode** - writes an ordered SQL baseline file for the discovered schema objects
- **Markdown copy report** - writes a post-run markdown summary with per-table copied row counts and run highlights
- **Drop-existing mode** - optionally drop matching target tables before recreating them
- **Fake data replacement** - replace configured column values during copy and data export using `gofakeit`
- **Portable Docker bundles** - snapshot a copied Docker database into a checksum-verified folder that can be moved to another computer and restored there
- **Terminal UI** - interactive Bubble Tea mode for entering source/target settings, include/exclude filters, exporting YAML config, and editing exact per-column fake-data rules
- **Post-data objects** - creates primary keys, checks, foreign keys, and indexes after data is loaded
- **Integration tested** - includes testcontainers-based integration tests

## Usage

Launch the application with:

```sh
mssql-copier
```

To start from a specific YAML file:

```sh
mssql-copier --config ./config/prod.yml
```

To execute a YAML configuration without opening the TUI:

```sh
mssql-copier run --config ./config/prod.yml
```

For a non-local copy target, the headless command prompts for explicit confirmation. In trusted automation, pass `--yes` to confirm the configured target without a prompt.

The TUI lets you enter source and target SQL Server connection parameters as separate fields such as server, port, database, user, password, encryption settings, and extra driver options. It also lets you adjust filters, report/export paths, Docker target settings, fake-data rules, and export the current state back into a YAML config file.

When a headless copy target is not local (`localhost`, `127.0.0.1`, or loopback IPv6 such as `::1`), the app asks for an explicit `yes` before it opens the target connection.

If the target DSN names a database that does not exist yet, the copier first connects to `master` on that same SQL Server instance and creates the database automatically before it opens the target connection.

Docker targets preserve the source database name. For example, copying from a source database named `testdb` creates and populates `testdb` in the Docker SQL Server instance. Copy mode rejects missing target database names and SQL Server system databases (`master`, `model`, `msdb`, and `tempdb`) as targets.

### Docker storage and portable bundles

For a Docker target, the TUI's `Storage` field cycles through three choices:

- `temporary` keeps the SQL Server files only in the container
- `local volume` uses the existing persistent named-volume workflow on this computer
- `portable bundle` uses a named volume while copying, then stops SQL Server briefly and creates a movable bundle folder

Portable mode follows the same archive format as the `shv-db-bundle` reference program. The output folder contains `docker-compose.yml`, `mssql_data.tar.gz`, `manifest.json` with a SHA-256 checksum, `README.txt`, and the current `mssql-copier` executable. The bundle directory must be missing or empty; an existing bundle is never overwritten.

Copy the entire folder to the destination computer, start Docker Desktop, open a terminal in that folder, and run:

```sh
./mssql-copier restore
```

On Windows PowerShell, run `./mssql-copier.exe restore`. Restore verifies the checksum, creates the Compose service and named volume, extracts the SQL Server files, and starts the service. It refuses to replace a non-empty destination volume unless you explicitly run `restore --force`. Use `restore --no-start` to leave the service stopped.

The same mode can be saved in YAML:

```yaml
docker:
  persistent: true
  portable: true
  compose-dir: ./docker-work
  bundle-dir: ./database-bundle
  port: 1433
```

The Compose file necessarily contains the generated SQL Server administrator password, so transfer and store the portable folder securely.

The form supports four run modes: `copy`, `plan`, `ddl`, and `ddl+data`. When target type is `local`, the TUI only accepts loopback target addresses such as `localhost`, `127.0.0.1`, or `::1`.

Inside the fake-data editor, the TUI shows copyable source columns and lets you assign exact `schema.table.column` faker rules from the supported `gofakeit` catalog. Functions with parameters can be configured directly in the TUI using semicolon-separated argument values in declared order. When an `llm` config is present and usable, the editor also exposes an auto-select action that asks the configured model to pre-fill faker choices for likely sensitive columns. Fake-data editing is available in `copy` and `ddl+data` modes.

### Plan mode (dry run)

Preview which objects would be copied without touching a target. In the TUI, switch to `plan` mode and provide source settings plus any filters you want to test. `drop-existing` remains available in this mode so the plan output still reflects recreate behavior.

### DDL export

Write a source-only Flyway SQL Server baseline migration for the selected schema objects. In the TUI, switch to `ddl` mode and fill in the DDL export path. The generated file is ordered T-SQL with `GO` batch delimiters, so programmable objects such as views, functions, procedures, and triggers are accepted by Flyway's SQL Server parser. This mode exports DDL only; it does not export table data.

Use a Flyway baseline or versioned migration filename such as `B001__initial_schema.sql` or `V001__initial_schema.sql`. The copier leaves the exact filename and version under your control.

The generated file contains ordered SQL statements for schemas, types, sequences, tables, constraints, indexes, views, functions, synonyms, procedures, and triggers. Because this is an initial baseline export, `drop-existing` is not supported with this mode and is hidden in the TUI.

### Data export

Write a source-only data seed file for the selected tables by switching to `ddl+data` mode. This mode generates both a DDL baseline file and a plain SQL data file with semicolon-terminated `SET IDENTITY_INSERT` and `INSERT` statements, with no `GO` batches.

The generated file contains deterministic table sections and row inserts ordered by primary key when available. It temporarily disables the source-defined constraints on the exported tables before loading rows, then restores each constraint's enabled and trusted state. `drop-existing` is not supported with this mode and is hidden in the TUI.

For integration testing, `ddl+data` mode also exposes an `export data rows` limit so you can cap the export to the first `N` rows per selected table.

The row cap starts from the deterministic per-table sample and then pulls in any referenced parent rows needed to keep copied foreign keys valid inside the exported set.

### Markdown copy report

Write a markdown summary after a successful copy run. The generated report includes overall totals, a few run highlights, and a per-table breakdown with copied rows, approximate source rows, copy mode, and notable details such as identity insert or bulk-copy fallback reasons.

In the TUI, the report path is shown only in `copy` mode. Because the report is based on actual copied rows, it cannot be combined with `plan`, `ddl`, or `ddl+data`.

### Filtering objects

Filters are applied by schema name and object name across copied tables and other discovered objects.

When a filtered copy excludes a referenced parent table and that table is not already present on the target, foreign key recreation for the copied child table is skipped.

Schema dependencies needed by selected objects are included automatically even when they fall outside an include filter. For example, selecting `sales.orders` also selects an alias type in `types` or a sequence in `sequences` when the table definition references it. A table pulled in only because a view or function references it is included in DDL but its data is not copied or exported. During a direct copy, an existing dependency-only target table is preserved. An explicit exclude filter still wins; the copier stops with a dependency error instead of producing an incomplete schema.

Configure these in the TUI or YAML:

- Copy only tables in the `sales` schema with `include-schemas: [sales]`
- Exclude audit tables with `exclude-tables: ["*.audit_%"]`
- Copy only specific tables with `include-tables: [dbo.orders, sales.customers]`

### Drop and recreate

Enable `drop-existing` in the TUI when you want to recreate matching target tables before loading data.

Alias user-defined types and user-defined table types are created before tables and procedures so recreated table definitions can keep alias types and TVP-based procedures can compile on the target. Sequences are created before tables so defaults like `NEXT VALUE FOR ...` work during table creation. Views are created after tables and indexes. Functions are created after views in dependency order. Synonyms are then recreated so late-bound references are available to programmable objects. Stored procedures are created after tables, views, functions, table types, and synonyms, with explicit dependency ordering across copied procedures. Table-scoped DML triggers are created after tables, procedures, and synonyms so they bind to recreated target tables without firing during the initial data copy and can reference copied synonyms. Existing target sequences, views, functions, procedures, triggers, and synonyms are refreshed on reruns. Alias types are recreated on rerun only when `--drop-existing` is set. Table types are created only when missing.

### Tuning

Adjust `workers` and `batch-size` in `copy` mode to tune concurrent copy throughput.

### YAML configuration

You can keep most parameters in a YAML file. By default, the copier looks for `mssql-copier.yml` in the current working directory.

A starter template is checked in at `mssql-copier.example.yml`.

```sh
# Uses ./mssql-copier.yml when present
mssql-copier

# Use a custom config file path
mssql-copier --config ./config/prod.yml
```

The app loads the selected YAML file before opening the TUI, so the form starts with your saved values.

`export-data-rows` only applies together with `export-data`, and in the TUI it is only shown in `ddl+data` mode.

`fake-data` can still be authored manually in YAML, but TUI-managed fake-data mappings are stored separately in `.mssql-copier/fake-data-mapping.yml` and keyed by the source database DSN. Each entry maps a column selector to a [`gofakeit`](https://github.com/brianvoe/gofakeit) function. Selectors support:

- exact column name: `name`
- exact table and column: `users.name`
- exact schema, table, and column: `dbo.users.name`
- regex: any selector that is not a plain identifier path is treated as a case-insensitive regex and matched against `column`, `table.column`, and `schema.table.column`. Example: `name.*`

Use `gofakeit` function names like `Email`, `FirstName`, or `LoremIpsumSentence`.

Parameters are optional and are appended after the function name using `;` in declared order. Examples: `LoremIpsumSentence;10` and `Price;1;100`.

The TUI currently writes exact per-column selectors only: `schema.table.column`. Regex, table-level, and column-level selectors remain editable in YAML.

The TUI can also export its current state to a YAML file path you choose on the form screen. This is useful for saving source/target settings, filters, run mode outputs such as report and export paths, and optional LLM settings before running. Fake-data mappings chosen in the TUI are stored in `.mssql-copier/fake-data-mapping.yml` under the current source DSN instead of being written into the exported YAML file.

Exported YAML never includes DSN passwords, literal LLM API keys, or Docker SA passwords. Use `llm.api-key-env` for LLM credentials. Docker passwords are generated when needed or recovered from the protected compose file for an existing persistent target.

Optional TUI LLM auto-selection is configured in YAML:

```yaml
llm:
  provider: openai
  model: gpt-4o-mini
  api-key-env: OPENAI_API_KEY
  base-url: https://api.openai.com/v1
```

`provider` currently supports `openai`. `api-key-env` is preferred so secrets stay out of the config file. Azure OpenAI can be used with `by-azure`, `base-url`, and `api-version`.

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
llm:
  provider: openai
  model: gpt-4o-mini
  api-key-env: OPENAI_API_KEY
  base-url: https://api.openai.com/v1
```

For a non-interactive `ddl+data` export, set both output paths and execute the config with `mssql-copier run --config <path>`:

```yaml
source: sqlserver://user:pass@source-host:1433?database=SourceDB
export-ddl: ./flyway/sql/B001__initial_schema.sql
export-data: ./export/data.sql
export-data-rows: 25
```

### Fake data replacement

Configured fake-data rules are applied in both copy mode and `ddl+data` mode before values are written to the target or serialized into SQL inserts.

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

The app validates every configured function and parameter list before execution and fails fast when a function name is unknown, parameters do not fit the selected function, or the function returns a complex value type that the copier cannot safely write to SQL Server.

In TUI mode, the fake-data picker only offers `gofakeit` functions whose output can already be written safely by the copier. Parameterized functions can be selected and configured directly from the TUI.

### DSN format

Uses the `go-mssqldb` driver. Examples:

```
sqlserver://user:password@host:1433?database=MyDB
sqlserver://user:password@host:1433?database=MyDB&encrypt=true&trustservercertificate=true
odbc:server=host;port=1433;database=MyDB;user id=user;password={a;complex;password}
```

ODBC-style braced values are supported, including passwords containing semicolons. Exported configuration and plan output remove passwords from URL user info, URL query parameters, and key-value parameters.

### Wildcard patterns

Filter arguments support SQL-style and glob-style wildcards:
- `*` or `%` - matches any sequence of characters
- `?` or `_` - matches exactly one character

Examples: `sales*`, `dbo.%`, `audit_202?`, `*_archive`

## How it works

1. **Discover** - queries `sys.tables`, `sys.types`, `sys.table_types`, `sys.sequences`, `sys.views`, `sys.objects`, `sys.procedures`, `sys.triggers`, `sys.synonyms`, `sys.columns`, `sys.indexes`, and other system catalog views on the source to build metadata for copied objects
2. **Filter** - applies include/exclude rules against schema names and object names
3. **Plan** (optional) - prints the planned actions and exits
4. **Create schemas** - creates any missing non-`dbo` schemas needed by copied objects on the target
5. **Prepare target** - optionally drops existing target tables
6. **Create alias types** - creates copied alias user-defined types before tables are recreated
7. **Create table types** - creates copied user-defined table types before dependent procedures are recreated
8. **Create sequences** - creates or updates copied sequences before tables are created
9. **Create tables** - generates and executes `CREATE TABLE` statements from source column definitions (including defaults, computed columns, collations, and preserved alias types)
10. **Copy data** - distributes tables across worker goroutines; each table is copied in a single transaction using bulk copy or row-insert depending on column type compatibility
11. **Post-data objects** - creates primary keys, check constraints, foreign keys, and indexes
12. **Create views** - creates or updates copied views in dependency order
13. **Create functions** - creates or updates copied SQL functions in dependency order
14. **Create synonyms** - recreates copied synonyms after referenced objects are in place
15. **Create procedures** - creates or updates copied stored procedures after their copied dependencies are in place
16. **Create triggers** - creates or updates copied table-scoped DML triggers after tables, procedures, and synonyms are in place

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

# Run the portable bundle/restore integration test (requires Docker)
task test:portable
```

Integration tests use [testcontainers-go](https://github.com/testcontainers/testcontainers-go) to spin up real SQL Server instances in Docker.

## License

MIT License. See [LICENSE](LICENSE) for details.
