#!/bin/sh
# code_canvas stub — 0.3.19 P9 internal pilot.
# End-to-end smoke for the Labs platform: manifest → catalog →
# registry → dispatcher → manager-factory → IPC. A 30-line noop
# HTTP server that returns 200 on /health and 404 elsewhere, so
# the loopback pipeline can be exercised without depending on a
# real external service. Real code-canvas experiments replace this
# stub with the actual binary.
#
# 0.3.51: removed `exec python3 - <<PY` — under `exec` the heredoc
# is consumed by the surrounding shell and `python3 -` reads empty
# stdin, so the server silently exits without binding the port.
# Running python3 in the foreground (no exec) keeps the heredoc
# attached to stdin and the server stays up. Also added SO_REUSEADDR
# so a prior binding in TIME_WAIT doesn't make the stub silently
# exit with EADDRINUSE on restart.
PORT="${1:-8091}"
python3 - <<PY
import http.server, socketserver, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health":
            self.send_response(200); self.end_headers()
            self.wfile.write(b'{"status":"ok","stub":"code_canvas"}')
        else:
            self.send_response(404); self.end_headers()
    def log_message(self, *a, **k): pass
class ReusableTCPServer(socketserver.TCPServer):
    allow_reuse_address = True
with ReusableTCPServer(("127.0.0.1", int("${PORT}")), H) as s:
    s.serve_forever()
PY