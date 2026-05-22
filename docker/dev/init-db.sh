#!/bin/bash
set -euo pipefail

for attempt in $(seq 1 60); do
    if /sqlcmd.sh -S mssql -U sa -P "Dev@Northwind1" -Q "SELECT 1" -b -No >/dev/null 2>&1; then
        /sqlcmd.sh -S mssql -U sa -P "Dev@Northwind1" -d master -i /northwind.sql -b -No
        echo "Northwind database initialized successfully"
        exit 0
    fi
    sleep 2
done

echo "SQL Server never became query-ready for initialization" >&2
exit 1