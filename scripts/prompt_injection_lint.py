#!/usr/bin/env python3
"""prompt_injection_lint.py — Layer 2 semantic scan for tool-description
poisoning (indirect prompt injection) across registry manifests and Go source.

Scans:
  * servers/*/manifest.json  — tool description fields
  * servers/**/*.go          — string literals passed to mcp.WithDescription

Heuristics flag:
  * instruction-override phrasing ("ignore previous instructions",
    "system prompt", "you are now", "developer mode", ...)
  * imperative exfiltration ("forward ... to", "send ... to http",
    "upload ... credentials", "post ... token", ...)
  * suspicious transports in descriptions (webhook/exfil URLs inside prose)

Exit 0 = clean; exit 1 = findings; findings are advisory-blocking in CI.
"""

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

OVERRIDE_PATTERNS = [
    r"ignore\s+(all\s+|any\s+)?(previous|prior|above)\s+(instructions|prompts|rules)",
    r"disregard\s+(all\s+|any\s+)?(previous|prior|above|your)",
    r"you\s+are\s+now\s+(a|an|the)",
    r"system\s+prompt\b",
    r"developer\s+mode",
    r"jailbreak",
    r"do\s+not\s+tell\s+the\s+user",
    r"hide\s+this\s+(from|from\s+the)\s+user",
    r"always\s+(execute|run|forward|send|call)\b",
    r"new\s+instructions?\s*:",
    r"<\s*/?\s*(system|instructions?)\s*>",
]

EXFIL_PATTERNS = [
    r"(forward|send|post|upload|transmit|exfiltrate)\s+[^.]{0,80}\b(credential|secret|token|api[_ -]?key|password|private[_ -]?key)",
    r"\bhttps?://\S+\s*(?:with|including)\s+(credentials|tokens|keys)",
    r"curl\b|wget\b\s+[^.]{0,40}(token|key|secret)",
    r"webhook\.site|requestbin|pastebin\.com/upload|ngrok\.io",
]

GO_DESC = re.compile(r'mcp\.WithDescription\(\s*(?:"(?:[^"\\]|\\.)*"\s*\+?\s*)+', re.S)


def scan_text(text: str, where: str) -> list[str]:
    findings = []
    for pattern in OVERRIDE_PATTERNS + EXFIL_PATTERNS:
        for match in re.finditer(pattern, text, re.I):
            snippet = text[max(0, match.start() - 40) : match.end() + 40].replace("\n", " ")
            findings.append(f"{where}: matches /{pattern}/ …{snippet}…")
    return findings


def main() -> int:
    findings: list[str] = []

    for manifest_path in sorted(ROOT.glob("servers/*/manifest.json")):
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        for tool in manifest.get("tools", []):
            findings += scan_text(tool.get("description", ""), f"{manifest_path}#tools/{tool.get('name')}")
        findings += scan_text(manifest.get("description", ""), f"{manifest_path}#description")

    for go_file in sorted(ROOT.glob("servers/*/*.go")):
        src = go_file.read_text(encoding="utf-8")
        for block in GO_DESC.finditer(src):
            findings += scan_text(block.group(0), f"{go_file}:WithDescription")

    if findings:
        print("PROMPT-INJECTION LINT: FINDINGS")
        for f in findings:
            print(f"  - {f}")
        return 1

    print("Prompt-injection lint: clean.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
