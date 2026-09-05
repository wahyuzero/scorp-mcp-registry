#!/usr/bin/env bash
# contract_check.sh — Layer 2 JSON-RPC contract validation for Scorp registry ports.
#
# For every server directory given (or all servers/ by default), this script:
#   1. Builds the Go source.
#   2. Boots the binary and performs a JSON-RPC 2.0 handshake:
#      initialize -> notifications/initialized -> tools/list
#   3. Verifies the server answers with protocolVersion, serverInfo,
#      and a non-empty tools array.
#
# Exit code 0 = all servers pass the contract check.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVERS_DIR="$ROOT/servers"
TARGETS=("$@")
if [ ${#TARGETS[@]} -eq 0 ]; then
    TARGETS=("$SERVERS_DIR"/*/)
fi

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail=0
for dir in "${TARGETS[@]}"; do
    dir="${dir%/}"
    name="$(basename "$dir")"
    [ -f "$dir/main.go" ] || continue
    echo "── Contract check: $name"

    bin="$WORKDIR/$name"
    (cd "$dir" && go build -o "$bin" .) || { echo "   ❌ build failed"; fail=1; continue; }

    # filesystem needs at least one allowed dir argument to boot
    args=( )
    [ "$name" = "filesystem" ] && args=("$WORKDIR")

    {
        printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"contract-check","version":"1.0.0"}}}'
        printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}'
        printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
    } | timeout 20 "$bin" "${args[@]+"${args[@]}"}" > "$WORKDIR/$name.out" 2>/dev/null || true

    if ! grep -q '"serverInfo"' "$WORKDIR/$name.out"; then
        echo "   ❌ no initialize response (serverInfo missing)"
        fail=1
        continue
    fi
    tools_line="$(grep -o '"tools":\[' "$WORKDIR/$name.out" | head -1 || true)"
    if [ -z "$tools_line" ]; then
        echo "   ❌ tools/list returned no tools array"
        fail=1
        continue
    fi
    count="$(grep -o '"name"' "$WORKDIR/$name.out" | wc -l)"
    echo "   ✅ handshake OK — tools advertised: $count"
done

if [ "$fail" -ne 0 ]; then
    echo "CONTRACT CHECK FAILED"
    exit 1
fi
echo "All servers passed the JSON-RPC contract check."
