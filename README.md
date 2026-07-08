# whatsapp_mcp

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go 1.25+](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](https://go.dev)
[![Python 3.11+](https://img.shields.io/badge/Python-3.11+-3776AB.svg)](https://www.python.org)
[![MCP](https://img.shields.io/badge/MCP-server-8A2BE2.svg)](https://modelcontextprotocol.io)

**An MCP server that puts your WhatsApp in your LLM's hands.**
Read, search, and send WhatsApp messages from your LLM — your chats become
context it can actually use, and it can reply, react, and share files for you.

Under the hood it's two small processes. A Go bridge pairs with your phone
once (QR scan, the official multi-device protocol) and quietly archives
every chat into a local SQLite database, exposing a token-guarded loopback
API for sending. A Python MCP server sits on top and gives the LLM
14 tools: search contacts and messages, list chats, send texts, quoted
replies, reactions, files, and voice notes.

**In short:**

1. Run the bridge, scan the QR code once.
2. Point your MCP client at the server.
3. Ask things like *"what did Anna say about the deposit?"* or
   *"send 'on my way' to +31 6 1234 5678"*.

Everything stays on your machine: messages live in local SQLite, the model
only sees what a tool call returns, and the send API is loopback-only
behind a bearer token. The usual caution applies — an LLM that can read
and send your messages is a prompt-injection target, so use it with
awareness.

## License

MIT — see [LICENSE](./LICENSE). This is a ground-up restructure of
[tr4m0ryp/whatsapp-mcp](https://github.com/tr4m0ryp/whatsapp-mcp), itself a
maintained fork of
[lharries/whatsapp-mcp](https://github.com/lharries/whatsapp-mcp); the
original copyright notices travel with it.
