"""Environment-driven configuration, resolved once at import.

Paths default to the sibling Go bridge's store directory so a plain
checkout works with zero configuration. Modules read these attributes at
call time (``config.MESSAGES_DB_PATH``), so tests can monkeypatch them.
"""

import os
from pathlib import Path

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
