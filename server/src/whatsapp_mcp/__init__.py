"""WhatsApp MCP server: MCP tool surface over the Go WhatsApp bridge."""

__all__ = ["main"]


def main() -> None:
    # Deferred so `import whatsapp_mcp` stays cheap and side-effect free.
    from .server import main as _main

    _main()
