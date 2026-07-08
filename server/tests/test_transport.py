"""Tests for the MCP transport env-var helpers."""

import pytest

from whatsapp_mcp.core.transport import resolve_host, resolve_port, resolve_transport


class TestResolveTransport:
    @pytest.mark.parametrize(
        ("value", "expected"),
        [
            (None, "stdio"),
            ("", "stdio"),
            ("  ", "stdio"),
            ("stdio", "stdio"),
            ("STDIO", "stdio"),
            ("http", "streamable-http"),
            ("streamable-http", "streamable-http"),
            ("streamable_http", "streamable-http"),
            ("sse", "sse"),
            (" SSE ", "sse"),
        ],
    )
    def test_valid_values(self, value, expected):
        assert resolve_transport(value) == expected

    def test_invalid_value_raises(self):
        with pytest.raises(ValueError, match="WHATSAPP_MCP_TRANSPORT"):
            resolve_transport("websocket")


class TestResolveHost:
    @pytest.mark.parametrize(
        ("value", "expected"),
        [
            (None, "127.0.0.1"),
            ("", "127.0.0.1"),
            ("  ", "127.0.0.1"),
            ("0.0.0.0", "0.0.0.0"),
            (" 192.168.1.10 ", "192.168.1.10"),
        ],
    )
    def test_values(self, value, expected):
        assert resolve_host(value) == expected


class TestResolvePort:
    @pytest.mark.parametrize(
        ("value", "expected"),
        [
            (None, 8000),
            ("", 8000),
            ("  ", 8000),
            ("8000", 8000),
            ("1", 1),
            ("65535", 65535),
        ],
    )
    def test_valid_values(self, value, expected):
        assert resolve_port(value) == expected

    def test_non_integer_raises(self):
        with pytest.raises(ValueError, match="must be an integer"):
            resolve_port("eight thousand")

    @pytest.mark.parametrize("value", ["0", "-1", "65536"])
    def test_out_of_range_raises(self, value):
        with pytest.raises(ValueError, match="between 1 and 65535"):
            resolve_port(value)
