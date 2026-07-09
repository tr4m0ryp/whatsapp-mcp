"""Pluggable auth layer for the MCP server (used only over http transport).

One function, ``build_auth``, selects the auth provider from config so the rest
of the server never branches on it. Modes, by ``MCP_OAUTH_PROVIDER``:

- ``"authkit"`` -- STATELESS OAuth via WorkOS AuthKit (the recommended
  claude.ai-web path): the server is a pure RFC 9728 resource server that
  verifies AuthKit-issued JWTs against the tenant JWKS and serves
  protected-resource metadata pointing clients at AuthKit. claude.ai registers
  itself with AuthKit (DCR must be enabled in the WorkOS dashboard) and refreshes
  tokens directly with AuthKit, so restarts never invalidate a connection.
  Needs ``WORKOS_AUTHKIT_DOMAIN`` + ``MCP_BASE_URL``.
- ``"workos"`` -- WorkOS OAuth proxy (``WorkOSProvider``): FastMCP does DCR for
  claude.ai and proxies login to AuthKit with a pre-registered client. STATEFUL
  (registrations live in-instance) -- prefer ``authkit`` on ephemeral hosts.
  Needs domain + ``WORKOS_CLIENT_ID`` + ``WORKOS_CLIENT_SECRET`` + base url, and
  ``<MCP_BASE_URL>/auth/callback`` registered in the WorkOS app.
- ``"oidc"`` -- any OIDC provider (Google, Auth0, Descope, ...) via ``OIDCProxy``.
  Needs ``MCP_OIDC_CONFIG_URL`` + ``MCP_OIDC_CLIENT_ID`` (+ secret) + base url.
- empty -- static bearer (Claude Code) when ``MCP_BEARER_TOKEN`` is set; else
  authless, which is refused over http unless ``MCP_ALLOW_UNAUTHENTICATED=true``.

Provider classes are imported lazily inside each branch so unused ones never
load and a missing optional dependency only bites the mode that needs it.
"""

from __future__ import annotations

import logging

from fastmcp.server.auth import AuthProvider
from fastmcp.server.auth.providers.jwt import StaticTokenVerifier

from whatsapp_mcp.core.config import Config

logger = logging.getLogger(__name__)

# Identity attached to the static bearer token (cosmetic; OAuth fills real ids).
_BEARER_CLIENT_ID = "whatsapp-mcp-session"


def build_auth(config: Config) -> AuthProvider | None:
    """Return the server's single auth layer, or ``None`` for authless dev.

    Over http transport an authless return is only allowed when
    ``mcp_allow_unauthenticated`` is set; otherwise this raises so a public
    server never comes up wide open (fail closed).
    """
    provider = config.mcp_oauth_provider
    if provider == "authkit":
        return _authkit(config)
    if provider == "workos":
        return _workos(config)
    if provider == "oidc":
        return _oidc(config)
    if provider:
        raise ValueError(
            f"Unknown MCP_OAUTH_PROVIDER={provider!r}; use 'authkit', 'workos', "
            "'oidc', or leave empty for bearer/authless.",
        )

    if config.mcp_bearer_token:
        return StaticTokenVerifier(
            tokens={
                config.mcp_bearer_token: {
                    "client_id": _BEARER_CLIENT_ID,
                    "scopes": [],
                },
            },
        )

    if config.mcp_transport == "http" and not config.mcp_allow_unauthenticated:
        raise ValueError(
            "http transport with no auth: set MCP_OAUTH_PROVIDER (e.g. authkit) "
            "or MCP_BEARER_TOKEN, or set MCP_ALLOW_UNAUTHENTICATED=true to run "
            "open on purpose (local dev only).",
        )
    logger.warning(
        "No auth configured -- starting AUTHLESS: every /mcp request is "
        "accepted. Configure auth before exposing this server.",
    )
    return None


def _require(config: Config, *field_names: str) -> None:
    missing = [f for f in field_names if not getattr(config, f, "")]
    if missing:
        env = {
            "workos_authkit_domain": "WORKOS_AUTHKIT_DOMAIN",
            "workos_client_id": "WORKOS_CLIENT_ID",
            "workos_client_secret": "WORKOS_CLIENT_SECRET",
            "mcp_base_url": "MCP_BASE_URL",
            "oidc_config_url": "MCP_OIDC_CONFIG_URL",
            "oidc_client_id": "MCP_OIDC_CLIENT_ID",
        }
        names = ", ".join(env.get(f, f) for f in missing)
        raise ValueError(
            f"MCP_OAUTH_PROVIDER={config.mcp_oauth_provider!r} requires: {names}",
        )


def _authkit(config: Config) -> AuthProvider:
    """Stateless OAuth via WorkOS AuthKit (``AuthKitProvider``)."""
    from fastmcp.server.auth.providers.jwt import JWTVerifier
    from fastmcp.server.auth.providers.workos import AuthKitProvider

    _require(config, "workos_authkit_domain", "mcp_base_url")
    domain = config.workos_authkit_domain.rstrip("/")
    logger.info(
        "Auth: AuthKit stateless resource server (domain=%s, resource=%s)",
        domain,
        config.mcp_base_url,
    )
    # Explicit verifier = issuer + signature only, no audience binding, matching
    # the proven finding-house / enrichment-mcp / gmail-mcp-server setup (an aud
    # check would also require the resource URL be registered as a Resource
    # Indicator in WorkOS).
    return AuthKitProvider(
        authkit_domain=domain,
        base_url=config.mcp_base_url,
        token_verifier=JWTVerifier(
            jwks_uri=f"{domain}/oauth2/jwks",
            issuer=domain,
            algorithm="RS256",
        ),
    )


def _workos(config: Config) -> AuthProvider:
    """WorkOS OAuth proxy (``WorkOSProvider``); stateful, needs a client id/secret."""
    from fastmcp.server.auth.providers.workos import WorkOSProvider

    _require(
        config,
        "workos_authkit_domain",
        "workos_client_id",
        "workos_client_secret",
        "mcp_base_url",
    )
    logger.info("Auth: WorkOS OAuth proxy (domain=%s)", config.workos_authkit_domain)
    return WorkOSProvider(
        client_id=config.workos_client_id,
        client_secret=config.workos_client_secret,
        authkit_domain=config.workos_authkit_domain,
        base_url=config.mcp_base_url,
    )


def _oidc(config: Config) -> AuthProvider:
    """OAuth via any OIDC provider (Google / Auth0 / Descope / ...)."""
    from fastmcp.server.auth.oidc_proxy import OIDCProxy

    _require(config, "oidc_config_url", "oidc_client_id", "mcp_base_url")
    logger.info("Auth: OIDC OAuth (config_url=%s)", config.oidc_config_url)
    return OIDCProxy(
        config_url=config.oidc_config_url,
        client_id=config.oidc_client_id,
        client_secret=config.oidc_client_secret or None,
        base_url=config.mcp_base_url,
    )


__all__ = ["build_auth"]
