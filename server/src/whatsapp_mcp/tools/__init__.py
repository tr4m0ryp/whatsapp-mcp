"""MCP tool surface: thin wrappers over the db and bridge layers.

Each submodule exposes plain module-level functions plus a TOOLS list;
register_all attaches them to a FastMCP instance. Keeping them as plain
functions (rather than closures created inside a registration call) lets
tests import and exercise them directly.
"""

from fastmcp import FastMCP

from . import chats, contacts, media, messages


def register_all(mcp: FastMCP) -> None:
    """Register every tool on the given FastMCP instance."""
    for module in (contacts, messages, chats, media):
        for fn in module.TOOLS:
            mcp.tool()(fn)


__all__ = ["chats", "contacts", "media", "messages", "register_all"]
