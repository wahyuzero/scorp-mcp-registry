# 🦂 scorp-mcp-registry

Official registry of **native Go MCP servers** for the [Scorp Agent](../scorp) marketplace.

Servers in this registry are single static Go binaries (startup < 20ms, ~5MB RAM)
built on [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) — a drop-in
low-footprint alternative to Node.js (`npx`) and Python (`uvx`) MCP runtimes.

## Layout

```
registry.json              # Aggregated index (health, tools, drift, artifacts)
schema/v1.json             # JSON Schema for scorp-mcp.json manifests
servers/<name>/            # One directory per server
  ├── main.go              # Fully auditable Go source (source-only policy)
  ├── manifest.json        # Attribution / health / security / drift metadata
  ├── go.mod / go.sum      # Pinned dependency graph
  └── README.md
scripts/                   # CI gates: manifest validation, injection lint, contract check
.github/workflows/         # security-audit, release-binaries, drift-livecheck
```

## Security shield (enforced by CI)

| Layer | Gate | Workflow |
| :--- | :--- | :--- |
| 1. Source-only registry | No binaries ever committed | `security-audit.yml` |
| 2. AST & prompt scan | gosec, govulncheck, injection linter | `security-audit.yml` |
| 3. Hermetic builds | Official artifacts built only by GitHub Actions | `release-binaries.yml` |
| 4. SHA-256 pinning | Verified client-side before execution (scorp-agent) | install flow |
| 5. Runtime redaction | Outbound secret redaction (scorp-agent `tools/redact.go`) | host runtime |

## Server lifecycle

* **Install (client side):** `scorp /mcp install <name>` — offers prebuilt binary
  (SHA-256 pinned), local rebuild from source, or the upstream Node/Python runtime.
* **Contribute a port:** PR with `servers/<name>/main.go` + `manifest.json`.
  All Layer 2 gates must pass again on every update.
* **Flavors/variants:** declare `variant` in the manifest
  (`name: sqlite-cipher`, canonical base `sqlite`).
* **Native Go originals:** `origin: "native"` — no upstream drift tracking.

## Local development

```bash
# Validate everything the CI would:
python3 scripts/validate_manifests.py
python3 scripts/prompt_injection_lint.py
./scripts/contract_check.sh

# Release: tag a server to trigger hermetic multi-arch builds
git tag fetch-v1.1.0 && git push origin fetch-v1.1.0
```

## License

MIT — each server directory carries upstream attribution in `manifest.json`.
Ports are community-driven Go reimplementations and are not affiliated with the
original upstream vendors.
