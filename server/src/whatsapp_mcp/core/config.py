"""Application configuration.

Two layers, both env-driven:

- Module-level path attributes (``MESSAGES_DB_PATH`` etc.), resolved once at
  import and read at call time by the db/bridge layers so tests can
  monkeypatch them. Paths default to the sibling Go bridge's store directory
  so a plain checkout works with zero configuration.
- A plain :class:`Config` dataclass for the MCP transport + auth surface,
  populated from environment variables via ``python-dotenv``. No pydantic.
  Every field maps to an env var whose name is the uppercased field name
  (``mcp_bearer_token`` -> ``MCP_BEARER_TOKEN``); an unset variable falls
  back to the field default. Values are coerced to the field's declared type.
"""

import os
from dataclasses import dataclass, fields
from pathlib import Path

from dotenv import load_dotenv

from .transport import resolve_transport

_DEFAULT_BRIDGE_STORE_DIR = Path(__file__).resolve().parents[4] / "bridge" / "store"

MESSAGES_DB_PATH = os.getenv(
    "WHATSAPP_DB_PATH",
    str(_DEFAULT_BRIDGE_STORE_DIR / "messages.db"),
)
WHATSMEOW_DB_PATH = os.getenv(
    "WHATSMEOW_DB_PATH",
    str(_DEFAULT_BRIDGE_STORE_DIR / "whatsapp.db"),
)
WHATSAPP_API_BASE_URL = os.getenv("WHATSAPP_API_URL", "http://localhost:8080/api")

_TRUE_TOKENS = frozenset({"1", "true", "yes", "on"})
_FALSE_TOKENS = frozenset({"0", "false", "no", "off"})


@dataclass
class Config:
    """MCP transport + auth knobs. Override any field via its uppercase env var."""

    # --- MCP transport ---
    # "stdio" (local, Claude Code) or "http" (remote, claude.ai).
    mcp_transport: str = "stdio"
    mcp_host: str = "0.0.0.0"
    mcp_port: int = 8000

    # --- Auth (only used for http transport) ---
    # Empty -> static bearer (if mcp_bearer_token set) or authless dev.
    # "authkit" -> stateless WorkOS AuthKit (recommended for claude.ai web).
    # "workos" -> WorkOS OAuth proxy. "oidc" -> any OIDC provider.
    mcp_oauth_provider: str = ""
    # Public https base of THIS server, WITHOUT the /mcp suffix -- what the
    # OAuth metadata advertises. Must match the claude.ai connector URL minus /mcp.
    mcp_base_url: str = ""
    # Static bearer accepted on /mcp (the Claude Code path); works alongside OAuth.
    mcp_bearer_token: str = ""
    # Fail closed: refuse to start authless over http unless this is set true.
    mcp_allow_unauthenticated: bool = False
    workos_authkit_domain: str = ""
    workos_client_id: str = ""
    workos_client_secret: str = ""
    oidc_config_url: str = ""
    oidc_client_id: str = ""
    oidc_client_secret: str = ""


def _coerce_bool(raw: str) -> bool:
    token = raw.strip().lower()
    if token in _TRUE_TOKENS:
        return True
    if token in _FALSE_TOKENS:
        return False
    raise ValueError(f"cannot parse boolean from {raw!r}")


# Keyed by the concrete type of each field's default. ``bool`` is checked before
# ``int`` would ever apply because dict lookup is by exact type, and
# ``type(True) is bool`` (never int).
_COERCERS = {
    bool: _coerce_bool,
    int: int,
    str: str,
}


def load_config() -> Config:
    """Build a :class:`Config` from the environment.

    Loads a ``.env`` file first (if present) via ``python-dotenv``, then reads
    each field's env var, coercing to the field's type. Unset variables keep
    their dataclass default.
    """
    load_dotenv()
    overrides: dict[str, object] = {}
    for field in fields(Config):
        raw = os.environ.get(field.name.upper())
        if raw is None:
            continue
        coerce = _COERCERS[type(field.default)]
        overrides[field.name] = coerce(raw)
    config = Config(**overrides)
    config.mcp_transport = resolve_transport(config.mcp_transport)
    # The OAuth metadata resource must have no trailing slash or /mcp suffix.
    config.mcp_base_url = config.mcp_base_url.rstrip("/")
    config.mcp_oauth_provider = config.mcp_oauth_provider.strip().lower()
    return config
