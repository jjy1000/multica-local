"""Boot the PYTHIA engine (FastAPI + Kalshi WS + autoscan loop)."""
from __future__ import annotations

import uvicorn

from .config import CONFIG


def main() -> None:
    uvicorn.run(
        "engine.server:app",
        host=CONFIG.engine_host,
        port=CONFIG.engine_port,
        log_level="info",
    )


if __name__ == "__main__":
    main()
