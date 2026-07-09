"""Tests for the pluggable auth layer (build_auth)."""

import pytest

from whatsapp_mcp.auth import build_auth
from whatsapp_mcp.core.config import Config


def test_no_auth_over_stdio_is_allowed():
    assert build_auth(Config()) is None


def test_http_without_auth_fails_closed():
    config = Config(mcp_transport="http")
    with pytest.raises(ValueError, match="http transport with no auth"):
        build_auth(config)


def test_http_authless_requires_explicit_opt_in():
    config = Config(mcp_transport="http", mcp_allow_unauthenticated=True)
    assert build_auth(config) is None


def test_static_bearer_mode():
    from fastmcp.server.auth.providers.jwt import StaticTokenVerifier

    config = Config(mcp_transport="http", mcp_bearer_token="secret-token-123")
    auth = build_auth(config)
    assert isinstance(auth, StaticTokenVerifier)


def test_unknown_provider_raises():
    config = Config(mcp_oauth_provider="magiclink")
    with pytest.raises(ValueError, match="Unknown MCP_OAUTH_PROVIDER"):
        build_auth(config)


def test_authkit_requires_domain_and_base_url():
    config = Config(mcp_transport="http", mcp_oauth_provider="authkit")
    with pytest.raises(ValueError, match="WORKOS_AUTHKIT_DOMAIN, MCP_BASE_URL"):
        build_auth(config)


def test_authkit_mode_builds_provider():
    from fastmcp.server.auth.providers.workos import AuthKitProvider

    config = Config(
        mcp_transport="http",
        mcp_oauth_provider="authkit",
        workos_authkit_domain="https://login.example.workos.dev",
        mcp_base_url="https://wa.example.com",
    )
    auth = build_auth(config)
    assert isinstance(auth, AuthKitProvider)


def test_oidc_requires_config_url_and_client_id():
    config = Config(mcp_transport="http", mcp_oauth_provider="oidc")
    with pytest.raises(ValueError, match="MCP_OIDC_CONFIG_URL, MCP_OIDC_CLIENT_ID"):
        build_auth(config)
