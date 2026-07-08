"""FastMCP app assembly and process entry point.

Importing this module never parses env vars or exits the process; all
env handling happens in main() so a bad setting can't break test imports.
"""

import os
import signal
import sys

from mcp.server.fastmcp import FastMCP

from .core.transport import resolve_host, resolve_port, resolve_transport
from .tools import register_all

mcp = FastMCP("whatsapp")
register_all(mcp)


def _shutdown_handler(signum, frame):
    """Handle shutdown signals gracefully to prevent zombie processes."""
    sys.exit(0)


def main() -> None:
    # Register signal handlers for clean shutdown
    signal.signal(signal.SIGINT, _shutdown_handler)
    signal.signal(signal.SIGTERM, _shutdown_handler)

    # Resolve the transport first: host/port are only used (and validated) for the
    # network transports, so a bad WHATSAPP_MCP_PORT can't break a stdio launch.
    # The localhost default keeps a remote server unreachable until explicitly opened up.
    try:
        transport = resolve_transport(os.getenv("WHATSAPP_MCP_TRANSPORT"))
        if transport != "stdio":
            mcp.settings.host = resolve_host(os.getenv("WHATSAPP_MCP_HOST"))
            mcp.settings.port = resolve_port(os.getenv("WHATSAPP_MCP_PORT"))
            # stdout is reserved for the protocol on stdio; log startup to stderr.
            print(
                f"WhatsApp MCP server listening on {mcp.settings.host}:{mcp.settings.port} via {transport}",
                file=sys.stderr,
            )
    except ValueError as exc:
        raise SystemExit(str(exc)) from None

    mcp.run(transport=transport)


if __name__ == "__main__":
    main()
