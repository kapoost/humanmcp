# humanMCP

Your personal content server that speaks the [Model Context Protocol](https://modelcontextprotocol.io).

Every human gets a node. AI agents and human visitors can browse your poems, essays, and notes. Some content is public. Some requires answering a question. Some will require payment (coming soon).

**You own the server. You own the content. You set the gates.**

## What it does

- Serves your Markdown content over MCP — any AI agent can `list_content`, `read_content`, `request_access`, `submit_answer`
- Challenge gates: lock a poem behind a question only the right person can answer
- Clean reading UI for human visitors (dark mode, serif typography)
- Owner editor: password-protected in-browser editor to write and publish
- Discoverable via `/.well-known/mcp-server.json`
- Single static Go binary — runs on Linux, Docker, Raspberry Pi, Fly.io, anything

## MCP tools

| Tool | Description |
|---|---|
| `get_author_profile` | Author name, bio, server info |
| `list_content` | All pieces with title, type, access level, description |
| `read_content` | Full body for public pieces |
| `request_access` | Gate info for locked pieces (challenge question or payment) |
| `submit_answer` | Answer a challenge to unlock a piece |

## Content format

Poems and essays live as plain Markdown files in `content/`:

```markdown
---
slug: the-river-knows
title: The River Knows
type: poem
access: locked
gate: challenge
challenge: What do rivers do that we cannot?
answer: forget
description: A poem about rivers and what we carry.
tags: [nature, grief]
published: 2024-04-01
---

The river carries everything downstream
and calls it moving on.
```

## Deploy to Fly.io

```bash
# Set secrets (do this once)
fly secrets set \
  EDIT_TOKEN=your-secret-password \
  AUTHOR_NAME="Your Name" \
  AUTHOR_BIO="Poet. Human. Node." \
  DOMAIN="your-app.fly.dev"

# Deploy
fly deploy
```

## Run locally

```bash
EDIT_TOKEN=secret AUTHOR_NAME="You" CONTENT_DIR=./content go run ./cmd/server/
# → http://localhost:8080
# → http://localhost:8080/mcp  (MCP endpoint)
```

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP port |
| `DOMAIN` | `localhost:8080` | Public domain (for well-known) |
| `AUTHOR_NAME` | `Anonymous` | Your name |
| `AUTHOR_BIO` | — | Short bio |
| `CONTENT_DIR` | `./content` | Path to content directory |
| `EDIT_TOKEN` | — | Secret token for owner access |

## Roadmap

- [x] MCP protocol (JSON-RPC 2.0)
- [x] Challenge gates (answer a question to unlock)
- [x] Owner web editor
- [ ] Lightning Network micropayments
- [ ] Peer-to-peer federation between humanMCP nodes
- [ ] Agent-to-agent bilateral content transactions
