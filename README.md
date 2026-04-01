# humanMCP

A personal content server speaking Model Context Protocol (MCP/JSON-RPC 2.0).

**Live:** https://kapoost-humanmcp.fly.dev  
**Author:** kapoost — sailor, poet, CTO

## What it is

humanMCP lets you publish poems, essays, notes, and typed data artifacts (images, contacts, datasets, vectors) and expose them to AI agents through a standard MCP interface. Agents can read your content, verify signatures, check licenses, and leave comments.

## MCP Tools (12)

| Tool | Description |
|---|---|
| `get_author_profile` | Who is kapoost |
| `list_content` | Browse all pieces |
| `read_content` | Read a piece (respects access gates) |
| `request_access` | Get gate details for locked content |
| `submit_answer` | Unlock challenge-gated content |
| `list_blobs` | Browse typed data artifacts |
| `read_blob` | Read image, contact, dataset, vector (respects audience) |
| `verify_content` | Verify Ed25519 signature |
| `get_certificate` | Full IP certificate: license, price, originality index, hash, signature |
| `request_license` | Declare intended use, get terms, logged for audit |
| `leave_comment` | Leave a reaction on a piece |
| `leave_message` | Send a direct note to the author |

## Connect

```json
{
  "mcpServers": {
    "kapoost": {
      "type": "http",
      "url": "https://kapoost-humanmcp.fly.dev/mcp"
    }
  }
}
```

## Content types

**Pieces** (Markdown files):
- Types: `poem`, `essay`, `note`, `audio`
- Access: `public` / `members` / `locked`
- Gates: `challenge` (Q&A), `time`, `manual`, `trade`
- Licenses: `free`, `cc-by`, `cc-by-nc`, `commercial`, `exclusive`, `all-rights`

**Blobs** (typed data):
- Types: `image`, `contact`, `vector`, `document`, `dataset`, `capsule`
- Audience: `[agent:claude, human:alice, agent:*]`

## Intellectual Property

Every piece is signed with Ed25519. `get_certificate` returns:
- SHA-256 content hash
- Ed25519 signature + public key
- **Originality Index** (0.0–1.0): burstiness (Fano Factor), lexical density (CTTR), Shannon entropy, structural signature
- License terms and price in sats (for commercial licenses)

## Stack

- Go 1.22, zero external dependencies
- Fly.io (region: waw), persistent volume
- Ed25519 signing (stdlib crypto)
- Plain Markdown files as database

## Limits

| Field | Limit |
|---|---|
| Message / comment text | 2000 chars |
| Blob inline text | 512 KB |
| File upload | 50 MB |
| Slug | 64 chars |
| Title | 256 chars |

## Run locally

```bash
go build ./cmd/server/
EDIT_TOKEN=secret AUTHOR_NAME=yourname ./server
```

## Deploy

```bash
fly launch --name yourname-humanmcp
fly secrets set EDIT_TOKEN=secret AUTHOR_NAME=yourname
fly deploy
```

## Keygen

```bash
go run ./cmd/keygen/
fly secrets set SIGNING_PRIVATE_KEY="..." SIGNING_PUBLIC_KEY="..."
```
