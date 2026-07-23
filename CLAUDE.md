# whatsapp_mcp -- Project Instructions

MCP server that lets an LLM read and send **WhatsApp** messages. Rebuild of
`tr4m0ryp/whatsapp-mcp` (a maintained fork of `lharries/whatsapp-mcp`, MIT),
restructured into domain-organized packages with identical behavior and a
fully ported test suite.

## Architecture (two processes)

1. **Go bridge** (`bridge/`) -- links to the WhatsApp account via whatsmeow
   (WhatsApp Web multi-device). Archives every message/chat/call into
   `bridge/store/messages.db` (SQLite), forwards inbound events to an
   optional webhook, and exposes a loopback REST API
   (`/api/send|react|download|health`) guarded by a bearer token +
   Host allow-list. Media is fetched on demand, never on arrival.
2. **Python MCP server** (`server/`) -- FastMCP app exposing 14 tools.
   Reads happen directly against the bridge's SQLite databases; writes
   (send message/file/audio/reaction, download media) go through the
   bridge REST API with the shared token.

The two meet at `bridge/store/`: the bridge writes it, the server reads it,
and `.bridge-token` inside it carries the shared REST credential.

## Layout

`bridge/` (Go, module `whatsapp-mcp/bridge`):
- `main.go` -- wire-up only; all logic lives in `internal/`
- `internal/config/` -- env parsing (port, FORWARD_SELF, store paths)
- `internal/auth/` -- bearer-token lifecycle + HTTP auth middleware
- `internal/media/` -- outbound media-path confinement (WHATSAPP_MEDIA_ROOTS)
- `internal/store/` -- messages.db schema, chats/messages/calls persistence,
  legacy-LID migrations
- `internal/wa/` -- whatsmeow glue: JID/LID resolution, content extraction,
  ephemeral (disappearing-message) handling, send, media download, live
  event handlers, history sync, connection/reconnect lifecycle
- `internal/api/` -- REST server, one file per endpoint
- `internal/webhook/` -- outbound webhook sender (token via X-Bridge-Token)
- `internal/ogg/` -- Ogg Opus duration/waveform analysis for voice notes
- `internal/testutil/` -- shared test fakes (mock LID store, in-memory DB)

`server/src/whatsapp_mcp/` (Python, package `whatsapp_mcp`):
- `core/` -- config (paths + MCP transport/auth dataclass), dataclasses,
  serialization
- `db/` -- read-side SQLite queries (identity/LID, messages, chats, contacts)
- `bridge/` -- REST client for the Go bridge (token discovery + POSTs)
- `media/` -- ffmpeg Opus/Ogg conversion for voice messages
- `tools/` -- `@mcp.tool` wrappers, thin over db/bridge; `TOOLS` lists +
  `register_all(mcp)`
- `auth.py` -- pluggable auth for http transport (WorkOS AuthKit / WorkOS
  proxy / OIDC / static bearer; fail-closed when exposed without auth)
- `server.py` -- FastMCP app; entry points `whatsapp-mcp` /
  `python -m whatsapp_mcp`

Every file < 300 lines; split into deeper subpackages before exceeding it.

## Running

```bash
# 1. Bridge. Pairing is opt-in and interactive-only; the first run needs it.
#    Prints the REST token once.
cd bridge && WHATSAPP_ALLOW_PAIRING=true go run .

# 2. MCP server (stdio by default; remote via MCP_TRANSPORT=http + OAuth,
#    same auth pattern as finding_house_mcp -- see .env.example)
cd server && uv run whatsapp-mcp
```

Config via env, documented in `.env.example`. Defaults assume both
components run from this checkout (server derives DB paths from the
sibling `bridge/store/`).

## Invariants

- **Chats are keyed by phone JID**, never `@lid`. Every inbound path
  (live events, history sync, sends) resolves LID -> phone before
  persisting; startup migrations rewrite legacy rows.
- **The bridge is the only WhatsApp session holder.** The Python side must
  never talk to WhatsApp directly -- reads via SQLite, writes via REST.
- **Fail-safe auth:** every `/api/*` request needs the bearer token; the
  webhook token is only attached to an explicitly configured WEBHOOK_URL,
  and outbound media paths are confined to WHATSAPP_MEDIA_ROOTS.
- **Unknown data stays honest:** adapters store NULL/empty over guesses;
  revoked messages keep content but gain `deleted_at`.
- No login-walled tricks beyond the official multi-device pairing; the
  store directory contains the session and must never be committed.

## Testing

```bash
cd bridge && go test ./... && go vet ./...
cd server && uv run pytest -q && uv run ruff check src tests
```

Go tests use `internal/testutil`; wa/api tests are external test packages
(`package x_test`), store/auth/media/webhook test their own package.
Python tests monkeypatch `whatsapp_mcp.core.config` attributes (read at
call time) instead of reloading modules.
