"""Fast smoke test for the PYTHIA engine source-of-record.

This guards the *vendor* copy (apps/desktop/vendor/pythia-src/engine), which
bundle-cli.mjs copies verbatim into apps/desktop/resources/pythia/engine on every
desktop bundle. Edit the vendor copy — never resources/ — or the change is lost.

Deliberately stdlib-only so it runs on any python3 without installing the engine's
runtime deps (fastapi/uvicorn/pydantic/...). It parses/compiles every module rather
than importing it, so a syntax error anywhere in the engine fails here with a
readable message before it can reach a bundle or a user's machine.

Runs two ways:
  * under pytest:      python3 -m pytest apps/desktop/vendor/pythia-src/tests
  * standalone (no deps): python3 apps/desktop/vendor/pythia-src/tests/test_smoke.py
"""
from __future__ import annotations

import ast
import re
from pathlib import Path

VENDOR_DIR = Path(__file__).resolve().parents[1]        # apps/desktop/vendor/pythia-src
ENGINE_DIR = VENDOR_DIR / "engine"
DESKTOP_DIR = Path(__file__).resolve().parents[3]        # apps/desktop
BUNDLE_CLI = DESKTOP_DIR / "scripts" / "bundle-cli.mjs"


def _engine_modules() -> list[Path]:
    return sorted(p for p in ENGINE_DIR.glob("*.py"))


def _pkg_name(spec: str) -> str:
    """Reduce a requirement spec to its bare, normalized package name.

    'uvicorn[standard]>=0.30' -> 'uvicorn'; 'python-dotenv>=1.0' -> 'python_dotenv'.
    """
    base = re.split(r"[\[<>=!~; ]", spec.strip(), maxsplit=1)[0]
    return base.strip().lower().replace("-", "_")


def test_engine_dir_exists() -> None:
    assert ENGINE_DIR.is_dir(), (
        f"PYTHIA engine source-of-record not found at {ENGINE_DIR}. "
        "The smoke test must run against the vendor copy, not resources/."
    )
    assert _engine_modules(), f"No .py modules found under {ENGINE_DIR}"


def test_engine_modules_parse() -> None:
    """Every engine module must parse and byte-compile cleanly."""
    failures: list[str] = []
    for module in _engine_modules():
        source = module.read_text(encoding="utf-8")
        try:
            tree = ast.parse(source, filename=str(module))
            compile(tree, filename=str(module), mode="exec")
        except SyntaxError as exc:  # noqa: PERF203 — want a per-file message
            where = f"{module.relative_to(VENDOR_DIR)}:{exc.lineno}"
            failures.append(f"  {where}: {exc.msg}")
    assert not failures, (
        "PYTHIA engine has syntax/compile errors (fix the vendor source):\n"
        + "\n".join(failures)
    )


def test_engine_package_has_version() -> None:
    """engine/__init__.py must declare __version__ (used by the desktop UI)."""
    init = ENGINE_DIR / "__init__.py"
    assert init.is_file(), f"Missing {init}"
    tree = ast.parse(init.read_text(encoding="utf-8"), filename=str(init))
    names = {
        t.id
        for node in tree.body
        if isinstance(node, ast.Assign)
        for t in node.targets
        if isinstance(t, ast.Name)
    }
    assert "__version__" in names, (
        f"{init.relative_to(VENDOR_DIR)} no longer declares __version__"
    )


def test_run_entrypoint_defines_main() -> None:
    """run.py is the boot entrypoint; it must keep a main() the wrapper can call."""
    run_py = ENGINE_DIR / "run.py"
    assert run_py.is_file(), f"Missing {run_py}"
    tree = ast.parse(run_py.read_text(encoding="utf-8"), filename=str(run_py))
    funcs = {n.name for n in tree.body if isinstance(n, ast.FunctionDef)}
    assert "main" in funcs, "engine/run.py must define a main() entrypoint"


def test_bundle_requirements_cover_vendor_requirements() -> None:
    """Bundle coverage guard.

    bundle-cli.mjs ships a *hardcoded* requirements list into the bundled engine —
    it does NOT read vendor/requirements.txt. So any runtime dependency declared in
    vendor/requirements.txt must also appear in the bundler's list, or the bundled
    engine ships without it and crashes on first import for end users.

    This asserts vendor requirements ⊆ bundler requirements (the dangerous
    direction). If bundle-cli.mjs can't be located it degrades to a printed hint so
    the smoke test still runs in trimmed checkouts.
    """
    req_file = VENDOR_DIR / "requirements.txt"
    if not req_file.is_file():
        print(f"[hint] no {req_file}; skipping bundle-coverage guard")
        return
    vendor_pkgs = {
        _pkg_name(line)
        for line in req_file.read_text(encoding="utf-8").splitlines()
        if line.strip() and not line.strip().startswith("#")
    }

    if not BUNDLE_CLI.is_file():
        print(f"[hint] {BUNDLE_CLI} not found; cannot verify bundle coverage")
        return
    text = BUNDLE_CLI.read_text(encoding="utf-8")
    # Anchor on `].join(` — the array holds specs like "uvicorn[standard]>=0.30"
    # whose inner ']' would otherwise end a naive non-greedy match early.
    match = re.search(r"const\s+requirements\s*=\s*\[(.*?)\]\s*\.join", text, re.DOTALL)
    assert match, (
        "Could not find the hardcoded `requirements` array in bundle-cli.mjs. "
        "If it was renamed, update this guard so bundle-coverage stays enforced."
    )
    bundler_pkgs = {_pkg_name(s) for s in re.findall(r'"([^"]+)"', match.group(1))}

    missing = sorted(vendor_pkgs - bundler_pkgs)
    assert not missing, (
        "bundle coverage risk: these deps are in vendor/requirements.txt but NOT in "
        "the hardcoded list in apps/desktop/scripts/bundle-cli.mjs, so the bundled "
        f"engine would ship without them: {missing}. "
        "Add them to the `requirements` array in bundle-cli.mjs."
    )


if __name__ == "__main__":
    import sys
    import traceback

    tests = [
        (name, obj)
        for name, obj in sorted(globals().items())
        if name.startswith("test_") and callable(obj)
    ]
    failed = 0
    for name, fn in tests:
        try:
            fn()
            print(f"  \033[32mPASS\033[0m {name}")
        except AssertionError as exc:
            failed += 1
            print(f"  \033[31mFAIL\033[0m {name}\n{exc}")
        except Exception:  # noqa: BLE001 — surface unexpected errors readably
            failed += 1
            print(f"  \033[31mERROR\033[0m {name}")
            traceback.print_exc()
    total = len(tests)
    print(f"\npythia smoke: {total - failed}/{total} passed")
    sys.exit(1 if failed else 0)
