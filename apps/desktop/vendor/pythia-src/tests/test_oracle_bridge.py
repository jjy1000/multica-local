"""Behavioural smoke for the PYTHIA oracle's LLM bridge (`Oracle._complete`).

Complements test_smoke.py (which is intentionally syntax/coverage-only and
never imports the engine). The 0.3.65 Pythia "direct Q&A returns nothing"
incident was a *behavioural* contract defect — the server's llm-call bridge
rejected the PAT the engine sends as `MULTICA_API_TOKEN` — which a parse-only
smoke cannot catch. This test exercises the real `_complete` branching without
installing the engine's heavy runtime deps (fastapi/pydantic/httpx/...) by
injecting tiny stub modules for the three third-party packages `oracle` pulls
in transitively (httpx, dotenv, pydantic), then asserts the loopback-bridge
contract the desktop build depends on:

  * MULTICA_REQUIRED=1 + URL + token  -> POST <URL>/api/runtime/llm-call with
    `Authorization: Bearer <token>` and `X-Pythia-Source: pythia-oracle`,
    and the returned `text` is surfaced.
  * MULTICA_REQUIRED=1 + URL + NO token  -> RuntimeError (fail loud, never
    silently fall back to an external LLM).
  * MULTICA_REQUIRED=1 + NO URL          -> RuntimeError.
  * MULTICA_REQUIRED unset (dev)         -> POST <base>/chat/completions with
    NO `X-Pythia-Source` header (legacy standalone path).

If a refactor breaks the stub assumptions (e.g. oracle starts needing a real
pydantic feature at import time) the import is skipped with a clear message
rather than failing the suite — the heavy, dependency-installed pytest run is
the right place for that, not the fast smoke.
"""
from __future__ import annotations

import asyncio
import importlib
import importlib.util
import os
import sys
import types
import unittest
from pathlib import Path

ENGINE_DIR = Path(__file__).resolve().parent.parent / "engine"


def _install_stubs() -> None:
    """Inject minimal stand-ins for dotenv, httpx, and pydantic."""

    # --- dotenv: load_dotenv becomes a no-op (tests control env directly). ---
    dotenv_stub = types.ModuleType("dotenv")
    dotenv_stub.load_dotenv = lambda *a, **k: False
    sys.modules["dotenv"] = dotenv_stub

    # --- httpx: record every request; return a canned 200. ---
    calls: list[dict] = []

    class _Resp:
        def __init__(self, payload: dict) -> None:
            self._payload = payload

        def raise_for_status(self) -> None:
            return None

        def json(self) -> dict:
            return self._payload

    class _Client:
        def __init__(self, *a, **k) -> None:
            pass

        async def __aenter__(self) -> "_Client":
            return self

        async def __aexit__(self, *a) -> None:
            return None

        async def post(self, url: str, json=None, headers=None) -> _Resp:
            calls.append({"method": "POST", "url": url, "json": json, "headers": dict(headers or {})})
            # The bridge path returns the Multica envelope ({"text": ...}); the
            # legacy dev path returns an OpenAI-shaped body ({"choices": [...]}).
            if url.endswith("/api/runtime/llm-call"):
                return _Resp({"text": "BRIDGE-OK"})
            return _Resp({"choices": [{"message": {"content": "DEV-OK"}}]})

        async def get(self, url: str, headers=None) -> _Resp:
            calls.append({"method": "GET", "url": url, "headers": dict(headers or {})})
            return _Resp({})

    httpx_stub = types.ModuleType("httpx")
    httpx_stub.AsyncClient = _Client
    sys.modules["httpx"] = httpx_stub

    # --- pydantic: permissive stand-in. `from __future__ import annotations`
    #     in models.py keeps annotations as strings, and oracle only needs the
    #     Prediction/WorldBrief *names* at import (it constructs Prediction in
    #     a code path this test never runs). A metaclass rewrites any subclass
    #     of our BaseModel into a plain permissive class so class-body
    #     `Field(...)` defaults never blow up. ---
    class _BaseModel:
        def __init__(self, *a, **k) -> None:
            for kk, vv in k.items():
                setattr(self, kk, vv)

    class _Meta(type):
        def __new__(mcs, name, bases, ns):  # noqa: N804
            if any(getattr(b, "__name__", None) == "_BaseModel" for b in bases) or any(
                isinstance(b, _Meta) for b in bases
            ):
                return type(name, (_BaseModel,), {"__init__": _BaseModel.__init__})
            return super().__new__(mcs, name, bases, ns)

    class BaseModel(_BaseModel, metaclass=_Meta):  # noqa: N801
        pass

    def _field(default=None, **k):  # noqa: ANN001
        return default

    pydantic_stub = types.ModuleType("pydantic")
    pydantic_stub.BaseModel = BaseModel
    pydantic_stub.Field = _field
    sys.modules["pydantic"] = pydantic_stub

    return calls


def _load_oracle():
    """Load engine.oracle with stubs in place; return (module, recorded_calls)."""
    calls = _install_stubs()
    pkg_root = ENGINE_DIR.parent
    if str(pkg_root) not in sys.path:
        sys.path.insert(0, str(pkg_root))
    # Build the `engine` package from the directory, then import oracle through
    # it so the relative `from .config import ...` resolves.
    pkg = importlib.util.module_from_spec(
        importlib.util.spec_from_loader("engine", loader=None, is_package=True)
    )
    pkg.__path__ = [str(ENGINE_DIR)]  # type: ignore[attr-defined]
    sys.modules["engine"] = pkg
    spec = importlib.util.spec_from_file_location(
        "engine.oracle", ENGINE_DIR / "oracle.py", submodule_search_locations=[]
    )
    assert spec and spec.loader
    mod = importlib.util.module_from_spec(spec)
    sys.modules["engine.oracle"] = mod
    spec.loader.exec_module(mod)
    return mod, calls


class CompleteBridgeContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        try:
            cls.oracle, cls.calls = _load_oracle()
        except Exception as exc:  # noqa: BLE001 — stub fragility must skip, not fail
            raise unittest.SkipTest(
                f"could not load oracle with dependency stubs ({exc!r}); "
                "run the dependency-installed pytest for full coverage"
            ) from exc

    def setUp(self) -> None:
        self.calls.clear()
        for k in ("MULTICA_REQUIRED", "MULTICA_AGENT_RUNTIME_URL", "MULTICA_API_TOKEN"):
            os.environ.pop(k, None)

    def _run(self, coro):
        return asyncio.get_event_loop().run_until_complete(coro) if False else asyncio.run(coro)

    def test_bridge_path_sends_pat_and_source_header(self) -> None:
        os.environ["MULTICA_REQUIRED"] = "1"
        os.environ["MULTICA_AGENT_RUNTIME_URL"] = "http://127.0.0.1:8090"
        os.environ["MULTICA_API_TOKEN"] = "mul_test_token"
        oracle = self.oracle.Oracle()
        text = self._run(oracle._complete([{"role": "user", "content": "hi"}]))
        self.assertEqual(text, "BRIDGE-OK")
        self.assertEqual(len(self.calls), 1)
        call = self.calls[0]
        self.assertEqual(call["url"], "http://127.0.0.1:8090/api/runtime/llm-call")
        self.assertEqual(call["headers"].get("Authorization"), "Bearer mul_test_token")
        self.assertEqual(call["headers"].get("X-Pythia-Source"), "pythia-oracle")

    def test_bridge_missing_token_fails_loud(self) -> None:
        os.environ["MULTICA_REQUIRED"] = "1"
        os.environ["MULTICA_AGENT_RUNTIME_URL"] = "http://127.0.0.1:8090"
        oracle = self.oracle.Oracle()
        with self.assertRaises(RuntimeError):
            self._run(oracle._complete([{"role": "user", "content": "hi"}]))
        self.assertEqual(self.calls, [], "must not contact any backend when token is missing")

    def test_bridge_missing_url_fails_loud(self) -> None:
        os.environ["MULTICA_REQUIRED"] = "1"
        os.environ["MULTICA_API_TOKEN"] = "mul_test_token"
        oracle = self.oracle.Oracle()
        with self.assertRaises(RuntimeError):
            self._run(oracle._complete([{"role": "user", "content": "hi"}]))
        self.assertEqual(self.calls, [], "must not contact any backend when URL is missing")

    def test_dev_fallback_omits_source_header(self) -> None:
        # No MULTICA_REQUIRED → legacy standalone path against CONFIG.llm_base_url.
        oracle = self.oracle.Oracle()
        text = self._run(oracle._complete([{"role": "user", "content": "hi"}]))
        self.assertEqual(text, "DEV-OK")
        self.assertEqual(len(self.calls), 1)
        call = self.calls[0]
        self.assertTrue(call["url"].endswith("/chat/completions"), call["url"])
        self.assertNotIn("X-Pythia-Source", call["headers"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
