"""Behavioral matrix for the PYTHIA engine loopback token guard.

Guards the *vendor* copy (apps/desktop/vendor/pythia-src/engine/server.py),
the same source-of-record as test_smoke.py. The guard is the security
boundary added with the 0.5.122 desktop batch: when PYTHIA_ENGINE_TOKEN is
set, every route except exactly /health must 401 without a matching
X-API-Key, and the MULTICA_API_TOKEN JWT is accepted as the alternative
credential the agent CLIs carry.

The guard reads its tokens at module import time, so this test drives the
middleware function directly (no uvicorn, no lifespan side effects) after
importing the engine module with a controlled environment. Skips when the
engine's runtime deps (fastapi/starlette) are absent — same convention as
the DB-backed Go tests skipping without DATABASE_URL.

Runs two ways:
  * under pytest:      python3 -m pytest apps/desktop/vendor/pythia-src/tests
  * standalone:        python3 apps/desktop/vendor/pythia-src/tests/test_token_guard.py
"""
from __future__ import annotations

import asyncio
import importlib
import os
import sys
from pathlib import Path

import pytest

VENDOR_DIR = Path(__file__).resolve().parents[1]  # apps/desktop/vendor/pythia-src

_ENGINE_TOKEN = "engine-token-0123456789abcdef"
_ENGINE_JWT = "jwt-fallback-token-0123456789abcdef"


def _call(guard, path: str, headers: list[tuple[bytes, bytes]]):
    """Invoke the ASGI middleware once and return the response."""
    from starlette.requests import Request
    from starlette.responses import PlainTextResponse

    async def call_next(_request):
        return PlainTextResponse("passed")

    scope = {
        "type": "http",
        "method": "GET",
        "path": path,
        "headers": headers,
        "query_string": b"",
        "scheme": "http",
        "server": ("127.0.0.1", 80),
    }
    return asyncio.run(guard(Request(scope), call_next))


def test_loopback_token_guard_matrix() -> None:
    pytest.importorskip("fastapi")
    from starlette.responses import JSONResponse, PlainTextResponse  # noqa: F401

    os.environ["PYTHIA_ENGINE_TOKEN"] = _ENGINE_TOKEN
    os.environ["MULTICA_API_TOKEN"] = _ENGINE_JWT
    sys.path.insert(0, str(VENDOR_DIR))
    try:
        import engine.server as server

        guard = server._loopback_token_guard
        assert server._ENGINE_TOKEN == _ENGINE_TOKEN, (
            "engine token must be captured from the env at import time"
        )

        def status(path: str, headers: list[tuple[bytes, bytes]]) -> int:
            return _call(guard, path, headers).status_code

        # Exactly /health is exempt; every other route is gated.
        assert status("/health", []) == 200, "/health must stay open"
        assert status("/health/", []) == 401, "/health/ is not the exemption"
        assert status("/HEALTH", []) == 401
        assert status("/config", []) == 401, "missing key must 401"

        # Wrong key, and the non-ASCII header regression: compare_digest on
        # str raises TypeError for chars >= 0x80 (Starlette decodes header
        # bytes as latin-1), which used to surface as a 500. The guard must
        # compare bytes and fail closed as 401.
        assert status("/config", [(b"x-api-key", b"wrong-token")]) == 401
        assert status("/config", [(b"x-api-key", b"t\xff")]) == 401

        # Both credentials are accepted: the manager-generated engine token
        # and the MULTICA_API_TOKEN JWT fallback the agent CLIs inherit.
        assert status("/config", [(b"x-api-key", _ENGINE_TOKEN.encode())]) == 200
        assert status("/config", [(b"x-api-key", _ENGINE_JWT.encode())]) == 200

        # Empty engine token env → open engine (dev / standalone runs).
        del os.environ["PYTHIA_ENGINE_TOKEN"]
        server = importlib.reload(server)
        assert server._ENGINE_TOKEN == "", "empty env must open the engine"
        assert _call(server._loopback_token_guard, "/config", []).status_code == 200
    finally:
        os.environ.pop("PYTHIA_ENGINE_TOKEN", None)
        os.environ.pop("MULTICA_API_TOKEN", None)
        if str(VENDOR_DIR) in sys.path:
            sys.path.remove(str(VENDOR_DIR))


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__]))
