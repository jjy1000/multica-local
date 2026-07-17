#!/bin/sh
# code_canvas stub — 0.3.19 P9 internal pilot.
# End-to-end smoke for the Labs platform: manifest → catalog →
# registry → dispatcher → manager-factory → IPC. A 30-line noop
# HTTP server that returns 200 on /health and 404 elsewhere, so
# the loopback pipeline can be exercised without depending on a
# real external service. Real code-canvas experiments replace this
# stub with the actual binary.
PORT="${1:-8091}"
exec python3 - <<PY
import http.server, socketserver, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health":
            self.send_response(200); self.end_headers()
            self.wfile.write(b'{"status":"ok","stub":"code_canvas"}')
        else:
            self.send_response(404); self.end_headers()
    def log_message(self, *a, **k): pass
with socketserver.TCPServer(("127.0.0.1", int("${PORT}")), H) as s:
    s.serve_forever()
PY
