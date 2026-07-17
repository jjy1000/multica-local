#!/usr/bin/env python3
"""
LLM Wiki Bridge MCP — stdio JSON-RPC server (0.3.27 B8).

The fork's MCP layer is still minimal: this server implements the
methods the 5-line audit surfaces (`list_tools`, `echo`, `vault_read`,
`vault_write`) plus a `health` probe, all speaking newline-delimited
JSON-RPC 2.0 over stdout/stdin (the canonical MCP transport).

Why a stub: the real LLM Wiki service lives at
`/Applications/LLM Wiki.app` outside the fork tree, and its MCP server
is bundled with the .app. The fork packages its own embedded stub so
the Labs platform can register the MCP server end-to-end (manifest →
catalog → registry → IPC dispatcher → manager-factory → subprocess)
without depending on the .app being installed. When LLM Wiki.app is
present the desktop manager prefers it; otherwise this stub stays up
and answers every request with a deterministic "not yet wired" payload.

0.3.27 B8 promises only the wire shape — actual tool impls (vector
search, graph queries, vault file reads/writes) land in a follow-up
release. The contract is stable enough that consumers written against
this stub will continue to work when the real handlers land.
"""
import json
import sys
import time

TOOLS = [
    {
        "name": "vault_read",
        "description": "Read a file from the LLM Wiki vault (stub: returns the path so consumers can detect the stub).",
        "inputSchema": {
            "type": "object",
            "properties": {"path": {"type": "string"}},
            "required": ["path"],
        },
    },
    {
        "name": "vault_write",
        "description": "Write a file to the LLM Wiki vault (stub: returns success without writing).",
        "inputSchema": {
            "type": "object",
            "properties": {"path": {"type": "string"}, "content": {"type": "string"}},
            "required": ["path", "content"],
        },
    },
    {
        "name": "echo",
        "description": "Echo input back to the caller — useful for MCP handshake diagnostics.",
        "inputSchema": {
            "type": "object",
            "properties": {"text": {"type": "string"}},
            "required": ["text"],
        },
    },
]


def reply(id_, result):
    return json.dumps({
        "jsonrpc": "2.0",
        "id": id_,
        "result": result,
    }, ensure_ascii=False) + "\n"


def err(id_, code, msg):
    return json.dumps({
        "jsonrpc": "2.0",
        "id": id_,
        "error": {"code": code, "message": msg},
    }, ensure_ascii=False) + "\n"


def dispatch(method, id_, params):
    if method == "initialize":
        return reply(id_, {
            "protocolVersion": "2024-11-05",
            "serverInfo": {"name": "llm-wiki-bridge-stub", "version": "0.3.28"},
            "capabilities": {"tools": {}},
        })
    if method == "notifications/initialized":
        return None  # no reply per MCP spec
    if method == "tools/list":
        return reply(id_, {"tools": TOOLS})
    if method == "tools/call":
        name = (params or {}).get("name")
        if name == "echo":
            return reply(id_, {
                "content": [{"type": "text", "text": (params.get("arguments") or {}).get("text", "")}],
            })
        if name == "vault_read":
            return reply(id_, {
                "content": [{"type": "text", "text": "[stub] would read: " + str((params.get("arguments") or {}).get("path", ""))}],
                "isError": False,
            })
        if name == "vault_write":
            return reply(id_, {
                "content": [{"type": "text", "text": "[stub] would write: " + str((params.get("arguments") or {}).get("path", ""))}],
                "isError": False,
            })
        return err(id_, -32601, f"tool not implemented in stub: {name}")
    if method == "health":
        return reply(id_, {"status": "ok", "service": "llm-wiki-bridge-stub"})
    return err(id_, -32601, f"method not implemented: {method}")


def main():
    started = time.time()
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except json.JSONDecodeError:
            sys.stdout.write(err(None, -32700, "parse error"))
            sys.stdout.flush()
            continue
        method = msg.get("method")
        id_ = msg.get("id")
        params = msg.get("params") or {}
        if method == "notifications/initialized":
            sys.stdout.write(json.dumps({"jsonrpc": "2.0", "method": "server/heartbeat", "params": {"uptime_ms": int((time.time() - started) * 1000)}}) + "\n")
            sys.stdout.flush()
            continue
        out = dispatch(method, id_, params)
        if out is not None:
            sys.stdout.write(out)
            sys.stdout.flush()


if __name__ == "__main__":
    sys.exit(main())
