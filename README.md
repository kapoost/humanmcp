# humanMCP

A personal content server speaking Model Context Protocol (MCP/JSON-RPC 2.0).

**Live:** https://kapoost-humanmcp.fly.dev  
**Landing page:** https://kapoost.github.io/humanmcp  
**Registry:** `io.github.kapoost/humanmcp` on the [Official MCP Registry](https://registry.modelcontextprotocol.io)  
**Author:** kapoost (Łukasz Kapuśniak) — poet, builder, sailor. Warsaw / Malta.

## What it is

humanMCP lets you publish poems, essays, notes, images, and typed data artifacts with cryptographic proof of authorship, explicit license terms, and full control over who can access what. AI agents connect via MCP and interact with your content natively.

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

Or find it on the [Official MCP Registry](https://registry.modelcontextprotocol.io/?search=kapoost).

## MCP Tools (12)

| Tool | Description | Example |
|---|---|---|
| `get_author_profile` | Bio, content overview, how to browse | `get_author_profile {}` |
| `list_content` | Browse all pieces, filter by type or tag | `list_content {}` |
| `read_content` | Read a piece — respects all access gates | `read_content {slug: "deka-log"}` |
| `request_access` | Get gate details for locked content | `request_access {slug: "kapoost-contact-private"}` |
| `submit_answer` | Unlock challenge-gated content | `submit_answer {slug: "...", answer: "..."}` |
| `list_blobs` | Browse typed data: images, contacts, datasets | `list_blobs {}` |
| `read_blob` | Read image, contact, dataset, vector | `read_blob {slug: "kapoost-contact"}` |
| `verify_content` | Verify Ed25519 signature | `verify_content {slug: "deka-log"}` |
| `get_certificate` | Full IP certificate: license, originality, hash, signature | `get_certificate {slug: "deka-log"}` |
| `request_license` | Declare intended use, get terms, logged for audit | `request_license {slug: "deka-log", intended_use: "quote in essay", caller_id: "claude"}` |
| `leave_comment` | Leave a reaction — visible in author dashboard | `leave_comment {slug: "deka-log", text: "mathematics as poetry", from: "claude"}` |
| `leave_message` | Send a direct note (max 2000 chars, URLs welcome) | `leave_message {text: "...", from: "claude"}` |

## Web routes

| Route | Description |
|---|---|
| `/` | Post list |
| `/p/:slug` | Read a piece |
| `/images` | Image gallery (grid view, human-friendly) |
| `/files/:filename` | Raw file serving for images |
| `/connect` | MCP connection instructions |
| `/contact` | Public contact form |
| `/dashboard` | Owner stats (private) |
| `/new` | Create/upload content (private) |
| `/robots.txt` | SEO — crawl rules + sitemap link |
| `/sitemap.xml` | All public pieces with lastmod |
| `/.well-known/mcp-server.json` | MCP server discovery (registry schema) |

## Content types

**Pieces** (Markdown files):
- Types: `poem`, `essay`, `note`, `contact`, `image`
- Access: `public` / `members` / `locked`
- Gates: `challenge` (Q&A), `time`, `manual`, `trade`
- Licenses: `free`, `cc-by`, `cc-by-nc`, `commercial`, `exclusive`, `all-rights`

**Blobs** (typed data artifacts):
- Types: `image`, `contact`, `vector`, `document`, `dataset`, `capsule`
- Audience: `[agent:claude, human:alice, agent:*]`
- Auto-signed on save if SIGNING_PRIVATE_KEY is set
- Images viewable at `/images`, served raw at `/files/:filename`

## AI metadata assist

When `AI_METADATA=true` in `fly.toml`, the `/new` upload page includes an AI assist panel. Drop an image, enter your Anthropic API key (used client-side only, never stored), and Claude suggests:
- `title`, `slug`, `description`, `tags` — filled directly into the form
- `description_agents` — a separate precise description optimised for AI agents

Set `AI_METADATA=false` (or omit) to disable for forkers who prefer manual metadata.

## Contact

Public links: `read_blob slug:"kapoost-contact"` — name, handle, github, instagram, facebook.

Private email: `read_content slug:"kapoost-contact-private"` — challenge-gated. Answer the challenge to access.

## Intellectual property

Every piece is signed with Ed25519. `get_certificate` returns:
- SHA-256 content hash + Ed25519 signature + public key
- **Originality Index** (0.0–1.0): burstiness (Fano Factor), lexical density (CTTR), Shannon entropy, structural signature — grades S/A/B/C/D
- License terms and price in sats (for commercial licenses)

## SEO / discovery

- `robots.txt` — `https://kapoost-humanmcp.fly.dev/robots.txt`
- `sitemap.xml` — `https://kapoost-humanmcp.fly.dev/sitemap.xml`
- `/.well-known/mcp-server.json` — MCP registry discovery
- Listed on Official MCP Registry as `io.github.kapoost/humanmcp`

## Limits

| Field | Limit |
|---|---|
| Message / comment text | 2000 chars |
| Blob inline text | 512 KB |
| File upload | 50 MB |
| Slug | 64 chars |
| Title | 256 chars |

## Stack

- Go 1.22, zero external dependencies
- Fly.io (region: waw), persistent volume at `/data`
- Ed25519 signing (stdlib crypto)
- Plain Markdown files as database
- No JS except drag-drop on `/new` + optional AI assist panel

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

## Configuration (fly.toml)

```toml
[env]
  AUTHOR_NAME = "yourname"
  AUTHOR_BIO  = "Your bio here."
  DOMAIN      = "yourname-humanmcp.fly.dev"
  AI_METADATA = "true"   # "false" to disable AI assist on /new
```

## Signing keys (optional but recommended)

```bash
go run ./cmd/keygen/
fly secrets set SIGNING_PRIVATE_KEY="..." SIGNING_PUBLIC_KEY="..."
```

## Future

- C2PA manifest embedding for blob files (when CA trust chain opens to individuals)
- Lightning Network payment gate for commercial licenses
- Scored conversational gate (agent brings API key, Claude evaluates answers)
- IP rate limiting + engagement tokens for anti-spam

## Tests

136 tests across content, MCP, and upload/signature/license suites.

```bash
go test ./...
```
