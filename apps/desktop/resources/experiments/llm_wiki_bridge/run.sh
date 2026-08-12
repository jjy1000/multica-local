#!/usr/bin/env python3
"""
LLM Wiki Bridge MCP — stdio JSON-RPC server (0.3.27 B8, real verbs 0.5.17).

The fork's MCP layer is minimal: this server implements the methods the
5-line audit surfaces (`list_tools`, `echo`, `vault_read`, `vault_write`)
plus a `health` probe, all speaking newline-delimited JSON-RPC 2.0 over
stdout/stdin (the canonical MCP transport).

Why this shape: the LLM Wiki service itself lives at
`/Applications/LLM Wiki.app` outside the fork tree. The vault verbs
forward to the multica backend over HTTP (/api/experimental/llm-wiki/*)
— the same surface the Go Skill adapter (`multica-llm-wiki`) uses — and
the backend owns the real work: the `llm_wiki_bridge` flag gate (403/404
when off), the desktop-API reads (search / read_file / files / graph),
and the vault-disk writes. This stub is the stdio transport bridge only;
0.3.27's deterministic "[stub] would read: …" payloads are gone.

Credentials are injected by the desktop manager at spawn time
(experimental/llm-wiki-bridge-manager.ts):
  MULTICA_API_URL    backend base, default http://127.0.0.1:8090
  MULTICA_API_TOKEN  optional Bearer token from the desktop profile
                     config.json (~/.multica/profiles/desktop-<host>/)
"""
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

API_BASE = os.environ.get("MULTICA_API_URL", "http://127.0.0.1:8090").rstrip("/")
TOKEN = os.environ.get("MULTICA_API_TOKEN", "")

TOOLS = [
    {
        "name": "vault_read",
        "description": "Read a file from the LLM Wiki vault via the multica backend.",
        "inputSchema": {
            "type": "object",
            "properties": {"path": {"type": "string"}},
            "required": ["path"],
        },
    },
    {
        "name": "vault_write",
        "description": "Write a file to the LLM Wiki vault via the multica backend.",
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


def _request(method, path, payload=None):
    """One HTTP call against the multica backend. Returns (status, body).

    HTTPError carries a response body (the backend's {"error": …} JSON),
    so it is returned as a normal (status, body) pair; only transport
    failures raise.
    """
    url = API_BASE + path
    data = None
    headers = {"Accept": "application/json"}
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    if TOKEN:
        headers["Authorization"] = "Bearer " + TOKEN
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except urllib.error.URLError as e:
        raise RuntimeError("multica backend unreachable on %s (%s)" % (API_BASE, e.reason))


def _tool_result(id_, status, body, verb):
    """Maps a backend response onto an MCP tools/call result."""
    try:
        parsed = json.loads(body) if body else None
    except ValueError:
        parsed = None
    if status >= 400:
        text = "llm-wiki %s failed: HTTP %s" % (verb, status)
        if parsed and isinstance(parsed, dict) and parsed.get("error"):
            text += ": " + str(parsed["error"])
        elif body:
            text += ": " + body[:200]
        if status == 404:
            text = "llm_wiki_bridge flag off or route not registered (404); enable the lab in Settings → Labs"
        elif status == 403:
            text = "llm_wiki_bridge flag off (403); enable the lab in Settings → Labs"
        return reply(id_, {"content": [{"type": "text", "text": text}], "isError": True})
    if verb == "read":
        content = parsed.get("content", "") if isinstance(parsed, dict) else ""
        return reply(id_, {"content": [{"type": "text", "text": content}]})
    # write — surface the absolute vault path so the caller can link it
    if isinstance(parsed, dict):
        text = "wrote %s (%s bytes)" % (
            parsed.get("absolute", parsed.get("path", "?")),
            parsed.get("bytes", "?"),
        )
    else:
        text = "wrote " + body
    return reply(id_, {"content": [{"type": "text", "text": text}]})


def dispatch(method, id_, params):
    if method == "initialize":
        return reply(id_, {
            "protocolVersion": "2024-11-05",
            "serverInfo": {"name": "llm-wiki-bridge", "version": "0.5.17"},
            "capabilities": {"tools": {}},
        })
    if method == "notifications/initialized":
        return None  # no reply per MCP spec
    if method == "tools/list":
        return reply(id_, {"tools": TOOLS})
    if method == "tools/call":
        name = (params or {}).get("name")
        args = (params or {}).get("arguments") or {}
        if name == "echo":
            return reply(id_, {
                "content": [{"type": "text", "text": str(args.get("text", ""))}],
            })
        if name == "vault_read":
            path = str(args.get("path", ""))
            if not path:
                return reply(id_, {"content": [{"type": "text", "text": "path is required"}], "isError": True})
            status, body = _request("GET", "/api/experimental/llm-wiki/read?path=" + urllib.parse.quote(path, safe=""))
            return _tool_result(id_, status, body, "read")
        if name == "vault_write":
            path = str(args.get("path", ""))
            content = str(args.get("content", ""))
            if not path:
                return reply(id_, {"content": [{"type": "text", "text": "path is required"}], "isError": True})
            status, body = _request("POST", "/api/experimental/llm-wiki/write", {"path": path, "content": content})
            return _tool_result(id_, status, body, "write")
        return err(id_, -32601, "tool not implemented: %s" % name)
    if method == "health":
        return reply(id_, {"status": "ok", "service": "llm-wiki-bridge"})
    return err(id_, -32601, "method not implemented: %s" % method)


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
        try:
            out = dispatch(method, id_, params)
        except RuntimeError as e:
            out = reply(id_, {"content": [{"type": "text", "text": str(e)}], "isError": True})
        if out is not None:
            sys.stdout.write(out)
            sys.stdout.flush()


if __name__ == "__main__":
    sys.exit(main())
