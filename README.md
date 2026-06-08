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

## MCP Tools (24)

### Content & IP

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
| `ask_human` | Ask kapoost a question requiring human judgement (returns id; poll `fetch_answer`). Open to any caller, rate-limited 5/hour/IP. | `ask_human {question: "Czy mogę cytować deka-log w eseju?", context: "academic"}` |
| `fetch_answer` | Retrieve kapoost's answer to a previous `ask_human`. Open to any caller, rate-limited 30 polls/hour/IP. | `fetch_answer {id: "20260608-2147-czy-moge"}` |

### Personas, Skills & Memory

| Tool | Description | Example |
|---|---|---|
| `bootstrap_session` | Unlock full private context with a session code | `bootstrap_session {code: "...", format: "full"}` |
| `list_personas` | Browse AI team roster (roles only, full prompts after bootstrap) | `list_personas {}` |
| `get_persona` | Read full persona prompt and configuration | `get_persona {slug: "ghost"}` |
| `list_skills` | Browse stored expertise catalog | `list_skills {}` |
| `get_skill` | Read a skill — detailed instructions for agents | `get_skill {slug: "mysloodsiewnia-architecture"}` |
| `upsert_skill` | Create or update a skill. Requires agent token. | `upsert_skill {slug: "...", category: "...", title: "...", body: "..."}` |
| `delete_skill` | Remove a skill by slug. Requires agent token. | `delete_skill {slug: "..."}` |
| `remember` | Store a memory under a session code (8KB/record). Session-gated. | `remember {text: "prefers terse responses", code: "<session>"}` |
| `recall` | Retrieve memories by session code, optional substring filter. Session-gated. | `recall {code: "<session>", query: "prefs"}` |
| `about_humanmcp` | Server self-description — endpoints, capabilities, first-contact orientation. | `about_humanmcp {}` |
| `about_humanmcp` | Server self-description for agent orientation | `about_humanmcp {}` |

## Web routes

### Public
| Route | Description |
|---|---|
| `/` | Home — list of pieces grouped by section, featured latest poem |
| `/p/:slug` | Read a piece (respects access gates) |
| `/p/:slug/translation/:lang` | Pre-rendered translation page |
| `/artworks` / `/artworks/:slug` | Artwork detail with provenance |
| `/images` | Image gallery (grid view) |
| `/gallery` | Alternate gallery layout |
| `/files/:filename` | Raw file serving for images and blobs |
| `/listings` / `/listings/:slug` | Listings stall + detail (sell/buy/offer/request/trade) |
| `/connect` | MCP connection instructions for agents |
| `/contact` | Public contact form (accepts `?regarding=<slug>` deep-links) |
| `/subscribe` / `/subscribe/confirm` | Subscribe to new listings (webhook or MCP channel) |
| `/team` / `/personas` | AI team roster |
| `/skills` | Skill catalog |
| `/for-agents` | Onboarding page for AI agents |
| `/rss.xml` | RSS 2.0 feed of public pieces |
| `/sitemap.xml` | All public pieces with lastmod |
| `/robots.txt` | SEO — crawl rules + sitemap link |
| `/stats` | Public read counters |
| `/llms.txt` | Plain-text overview for LLM clients |
| `/.well-known/mcp-server.json` | MCP server discovery (registry schema) |
| `/.well-known/agent.json` | A2A agent card |

### Owner-only (auth: `EDIT_TOKEN`)
| Route | Description |
|---|---|
| `/dashboard` | Stats + session code + message inbox |
| `/mc` | Mission Control (extended dashboard with windowed stats) |
| `/messages` | Inbox of submitted contact-form messages |
| `/new` | Create a new piece |
| `/edit/:slug` | Edit an existing piece |
| `/delete/:slug` | Delete a piece (POST) |
| `/listings/new` / `/listings/edit/:slug` / `/listings/delete/:slug` | Listing CRUD |
| `/timestamp/:slug` | OpenTimestamps stamp / upgrade (single piece) |
| `/timestamp-all` | Bulk-stamp every signed piece |
| `/questions` / `/questions/answer` | Inbox of `ask_human` questions + answer form |
| `/upload` | Blob upload (images, contacts, datasets) |
| `/llms-edit` | Edit the custom llms.txt |
| `/api/content`, `/api/content/`, `/api/skills`, `/api/blobs`, `/api/messages/` | Owner JSON APIs |

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

**Before every `fly deploy`:** commit your code (`git status` should be clean for tracked files). The Docker build bundles `internal/` and `content/` from the working tree — if you deploy with uncommitted changes and the image is later GC'd, that code is lost. Treat `git commit` + `git push` as part of the deploy procedure, not optional.

**After every deploy with template or struct changes:** check for silent template errors. Go's `html/template` renders a partial HTML and returns 200 OK on missing fields rather than failing loud. Catch them with:

```bash
fly logs -a yourname-humanmcp | grep "template error"
```

## Operational safety — Hodor

The server ships with a guardian persona **Hodor** and a public skill `operational-safety-public`. Both are accessible **without** `bootstrap_session` — so any MCP client connecting to this server reads the rules from the first command. The full incident-specific history lives in `operational-safety-private` (gated).

- `internal/mcp/handler.go::handleInitialize` injects an `OPERATIONAL SAFETY` block into the MCP server instructions.
- `bootstrap_session` returns a `=== GUARDIAN — LOAD FIRST ===` block at the top of its response, before any other persona or skill.
- The guardian rules cover: never print secrets to terminal, don't paste multi-line shell commands with backslash-continuation, default-deny destructive commands, rotation/audit order after a leak.

This is a personal pattern — your fork can keep it, modify it, or remove the persona and rely on a generic guardian. The mechanism (server instructions visible without bootstrap) is the load-bearing piece.

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

## Clients

| Client | Description | Link |
|---|---|---|
| Claude Desktop / Cursor | Direct MCP connection | [Connect instructions](/connect) |
| RPG Client | jRPG-styled browser interface (FF7 PS1 vibes) | [Play](https://kapoost.github.io/humanmcp-rpg) · [Source](https://github.com/kapoost/humanmcp-rpg) |

The RPG client works with any humanMCP instance — just enter the server URL on the connect screen.

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
