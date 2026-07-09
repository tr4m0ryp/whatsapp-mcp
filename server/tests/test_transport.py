"""Tests for the MCP transport/config env handling."""

import pytest

from whatsapp_mcp.core.config import Config, load_config
from whatsapp_mcp.core.transport import resolve_transport


class TestResolveTransport:
    @pytest.mark.parametrize(
        ("value", "expected"),
        [
            (None, "stdio"),
            ("", "stdio"),
            ("  ", "stdio"),
            ("stdio", "stdio"),
            ("STDIO", "stdio"),
            ("http", "http"),
            ("streamable-http", "http"),
            ("streamable_http", "http"),
            ("sse", "sse"),
            (" SSE ", "sse"),
        ],
    )
    def test_valid_values(self, value, expected):
        assert resolve_transport(value) == expected

    def test_invalid_value_raises(self):
        with pytest.raises(ValueError, match="MCP_TRANSPORT"):
            resolve_transport("websocket")


class TestLoadConfig:
    def test_defaults(self, monkeypatch):
        for var in ("MCP_TRANSPORT", "MCP_HOST", "MCP_PORT", "MCP_OAUTH_PROVIDER"):
            monkeypatch.delenv(var, raising=False)

        config = load_config()

        assert config.mcp_transport == "stdio"
        assert config.mcp_host == "0.0.0.0"
        assert config.mcp_port == 8000
        assert config.mcp_oauth_provider == ""
        assert config.mcp_allow_unauthenticated is False

    def test_env_overrides_and_normalization(self, monkeypatch):
        monkeypatch.setenv("MCP_TRANSPORT", "streamable-http")
        monkeypatch.setenv("MCP_PORT", "9000")
        monkeypatch.setenv("MCP_OAUTH_PROVIDER", " AuthKit ")
        monkeypatch.setenv("MCP_BASE_URL", "https://wa.example.com/")
        monkeypatch.setenv("MCP_ALLOW_UNAUTHENTICATED", "true")

        config = load_config()

        assert config.mcp_transport == "http"
        assert config.mcp_port == 9000
        # Provider is lowercased/stripped; base url loses its trailing slash.
        assert config.mcp_oauth_provider == "authkit"
        assert config.mcp_base_url == "https://wa.example.com"
        assert config.mcp_allow_unauthenticated is True

    def test_invalid_transport_raises(self, monkeypatch):
        monkeypatch.setenv("MCP_TRANSPORT", "websocket")
        with pytest.raises(ValueError, match="MCP_TRANSPORT"):
            load_config()

    def test_invalid_bool_raises(self, monkeypatch):
        monkeypatch.setenv("MCP_ALLOW_UNAUTHENTICATED", "maybe")
        with pytest.raises(ValueError, match="boolean"):
            load_config()

    def test_config_is_plain_dataclass(self):
        # Design contract: plain dataclass + os.environ, no pydantic.
        assert Config().mcp_transport == "stdio"
