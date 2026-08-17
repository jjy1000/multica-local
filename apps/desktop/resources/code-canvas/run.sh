#!/bin/sh
# code_canvas service — 0.5.18: replaced the /health-only stub with a real
# stdlib-only data plane.
#
# The desktop subprocess-manager spawns this script with the free loopback
# port as $1 (see BaseExperimentalManager.start: args = [...cfg.args,
# String(port)]), health-checks GET /health, then registers the URL with the
# same-origin reverse proxy mounted at /experimental/code-canvas.
#
# Endpoints:
#   GET  /health                                   → 200 {"status":"ok",...}
#   GET  /render?code=...&language=...             → 200 self-contained HTML
#   POST /render  {"code": "...", "language": "…"} → 200 self-contained HTML
#
# 0.3.51 lesson preserved: python3 runs in the FOREGROUND (no exec) so the
# heredoc stays attached to stdin and the server keeps serving. The quoted
# delimiter ('PY') blocks shell expansion inside the script.
# MUST assign PORT (not `${PORT:-…}`-defaulted): BaseExperimentalManager.start
# always passes the picked port as $1, so the manager's spawn-env's inherited
# PORT=8080 leak (from the user's dev shell) is clobbered here. If this ever
# becomes `${PORT:-"$1"}` or similar, the parent GUI's PORT leaks through
# and code-canvas binds to the wrong port.
if [ -n "$1" ]; then PORT="$1"; else PORT=8091; fi
export PORT
python3 - <<'PY'
import html
import json
import os
import re
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(os.environ["PORT"])
MAX_CODE_BYTES = 200_000

# language -> single-line comment prefix (used by the deterministic highlighter)
COMMENT_PREFIX = {
    "python": "#", "py": "#", "ruby": "#", "bash": "#", "sh": "#", "shell": "#",
    "javascript": "//", "js": "//", "typescript": "//", "ts": "//",
    "go": "//", "java": "//", "c": "//", "cpp": "//", "rust": "//",
    "kotlin": "//", "swift": "//", "php": "//",
    "sql": "--", "json": "",
}

# languages whose comment runs to a block terminator instead of end-of-line
COMMENT_BLOCK_END = {
    "css": "*/", "html": "-->", "xml": "-->", "markdown": "-->",
}

KEYWORDS = frozenset("""
    and as assert async await break class continue def del elif else except
    finally for from global if import in is lambda nonlocal not or pass raise
    return try while with yield let const var function new typeof instanceof
    void switch case default do package func go defer chan select struct
    interface map range true false nil None True False self static final
    int long double float char bool public private
""".split())


def highlight(code, language):
    """Deterministic stdlib-only tokenizer. Every emitted fragment is
    html.escaped before wrapping, so code can never inject markup."""
    prefix = COMMENT_PREFIX.get(language, "#")
    if language in COMMENT_BLOCK_END:
        comment_re = re.escape(prefix) + ".*?" + re.escape(COMMENT_BLOCK_END[language])
    else:
        comment_re = re.escape(prefix) + "[^\\n]*"
    # strings first (double / single / backtick), then comments, numbers,
    # identifiers, then the single-char fallback. re.S lets block comments
    # and triple-less strings span lines via the fallback.
    string_re = '"[^"]*"|' + "'" + "[^']*" + "'" + "|`[^`]*`"
    token_re = re.compile(
        "(?P<str>" + string_re + ")"
        "|(?P<cmt>" + comment_re + ")"
        "|(?P<num>(?<![A-Za-z0-9_])[0-9]+(?:[.][0-9]+)?(?![A-Za-z0-9_]))"
        "|(?P<word>[A-Za-z_][A-Za-z0-9_]*)"
        "|(?P<other>.)",
        re.S,
    )
    out = []
    for m in token_re.finditer(code):
        kind = m.lastgroup
        esc = html.escape(m.group())
        if kind == "str":
            out.append('<span class="tok-s">' + esc + "</span>")
        elif kind == "cmt":
            out.append('<span class="tok-c">' + esc + "</span>")
        elif kind == "num":
            out.append('<span class="tok-n">' + esc + "</span>")
        elif kind == "word":
            if m.group() in KEYWORDS:
                out.append('<span class="tok-k">' + esc + "</span>")
            else:
                out.append(esc)
        else:
            out.append(esc)
    return "".join(out)


def render_html(code, language):
    lang_label = html.escape(language or "text")
    svg = (
        '<svg class="canvas-mark" viewBox="0 0 64 64" width="64" height="64" aria-hidden="true">'
        '<rect x="8" y="8" width="48" height="48" rx="10" fill="none" stroke="currentColor"'
        ' stroke-width="2" opacity="0.35"/>'
        '<path d="M24 20l-8 12 8 12M40 20l8 12-8 12" fill="none" stroke="currentColor"'
        ' stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"/>'
        "</svg>"
    )
    return (
        "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\">"
        "<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">"
        "<title>Code Canvas</title><style>"
        "body{margin:0;background:#0f172a;color:#e2e8f0;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;"
        "display:flex;justify-content:center;padding:40px 16px}"
        ".canvas{max-width:920px;width:100%;background:#1e293b;border:1px solid #334155;border-radius:14px;"
        "overflow:hidden;box-shadow:0 18px 50px rgba(0,0,0,.45)}"
        ".canvas-head{display:flex;align-items:center;gap:12px;padding:12px 16px;background:#0f172a;"
        "border-bottom:1px solid #334155;color:#94a3b8;font-size:12px}"
        ".dot{width:10px;height:10px;border-radius:50%;display:inline-block}"
        ".dot-r{background:#f87171}.dot-y{background:#fbbf24}.dot-g{background:#34d399}"
        ".lang{margin-left:auto;background:#334155;color:#e2e8f0;padding:2px 10px;border-radius:999px;"
        "font-size:11px;text-transform:uppercase;letter-spacing:.08em}"
        ".canvas-code{margin:0;padding:20px 22px;overflow:auto;font-size:13.5px;line-height:1.65}"
        ".tok-k{color:#c4b5fd;font-weight:600}.tok-s{color:#86efac}.tok-c{color:#64748b;font-style:italic}"
        ".tok-n{color:#fdba74}"
        ".canvas-foot{display:flex;align-items:center;gap:10px;padding:10px 16px;border-top:1px solid #334155;"
        "color:#64748b;font-size:11px}"
        ".canvas-mark{color:#64748b;flex-shrink:0}"
        "</style></head><body>"
        '<div class="canvas">'
        '<div class="canvas-head"><span class="dot dot-r"></span><span class="dot dot-y"></span>'
        '<span class="dot dot-g"></span><span>code_canvas</span>'
        '<span class="lang">' + lang_label + "</span></div>"
        '<pre class="canvas-code"><code>' + highlight(code, language) + "</code></pre>"
        '<div class="canvas-foot">' + svg + "<span>Rendered by code_canvas · stdlib-only</span></div>"
        "</div></body></html>"
    )


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        path = self.path.split("?", 1)[0]
        if path == "/health":
            body = json.dumps({"status": "ok", "service": "code_canvas"}).encode()
            self._json(200, body)
            return
        if path == "/render":
            from urllib.parse import parse_qs, urlparse
            qs = parse_qs(urlparse(self.path).query)
            self._serve_render(qs.get("code", [""])[0], qs.get("language", ["text"])[0])
            return
        self.send_response(404)
        self.end_headers()

    def do_POST(self):
        path = self.path.split("?", 1)[0]
        if path != "/render":
            self.send_response(404)
            self.end_headers()
            return
        length = int(self.headers.get("Content-Length", "0"))
        if length <= 0 or length > MAX_CODE_BYTES + 4096:
            self._json(400, b'{"error":"empty or oversized body"}')
            return
        raw = self.rfile.read(length)
        try:
            payload = json.loads(raw.decode("utf-8"))
        except Exception:
            self._json(400, b'{"error":"invalid JSON body"}')
            return
        self._serve_render(payload.get("code", ""), payload.get("language", "text"))

    def _serve_render(self, code, language):
        if not isinstance(code, str):
            self._json(400, b'{"error":"code must be a string"}')
            return
        if len(code.encode("utf-8")) > MAX_CODE_BYTES:
            self._json(400, b'{"error":"code exceeds size limit"}')
            return
        if not isinstance(language, str):
            language = "text"
        page = render_html(code, language).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(page)))
        self.end_headers()
        self.wfile.write(page)

    def _json(self, status, body):
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *a, **k):
        pass


class ReusableServer(ThreadingHTTPServer):
    allow_reuse_address = True


if __name__ == "__main__":
    server = ReusableServer(("127.0.0.1", PORT), Handler)
    server.serve_forever()
PY
