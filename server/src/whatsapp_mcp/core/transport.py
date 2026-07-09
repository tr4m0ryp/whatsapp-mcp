"""Side-effect-free helper for the MCP_TRANSPORT env var."""

# Accepted MCP_TRANSPORT values mapped to FastMCP transport names.
# "http" is FastMCP's name for the spec's streamable-http transport, so the
# spec spelling is accepted as an alias.
TRANSPORT_ALIASES = {
    "stdio": "stdio",
    "http": "http",
    "streamable-http": "http",
    "streamable_http": "http",
    "sse": "sse",
}


def resolve_transport(value: str | None) -> str:
    """Map an MCP_TRANSPORT value to a FastMCP transport name.

    Unset or whitespace-only values default to "stdio".
    Raises ValueError for unrecognized values.
    """
    normalized = (value or "").strip().lower() or "stdio"
    try:
        return TRANSPORT_ALIASES[normalized]
    except KeyError:
        accepted = ", ".join(sorted(TRANSPORT_ALIASES))
        raise ValueError(
            f"Invalid MCP_TRANSPORT={value!r}; recommended values: stdio, http, sse "
            f"(http is the spec's streamable-http transport; all accepted inputs: {accepted})"
        ) from None
