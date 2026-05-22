#!/bin/bash
set -euo pipefail

if command -v sqlcmd >/dev/null 2>&1; then
    exec sqlcmd "$@"
fi

for candidate in \
    /opt/mssql-tools18/bin/sqlcmd \
    /opt/mssql-tools/bin/sqlcmd
do
    if [ -x "$candidate" ]; then
        exec "$candidate" "$@"
    fi
done

echo "sqlcmd was not found in the container image" >&2
exit 1