# humanmcp-go — Recovery Plan

**Created:** 2026-05-20
**Branch:** `hodor-recovery`
**Production status:** `kapoost-humanmcp` rolled back to image v273 (deployment-01KS0XZA36129RGDMQCNP40K29). Serving wczorajsza wersja UI.

## Co się stało

Lokalny `master` zawierał ostatni commit `62edede` z **15 kwietnia**. Od tego czasu rozwój kodu (Mission Control, artworks, listings, multi-template UI z keyboard shortcuts, dark/light theme, PL/EN i18n) odbywał się **bez commitów do gita** — kod żył tylko w binarce Docker na Fly. Filter-branch z 19 maja (`pre-email-rewrite-20260519`) przepisał meta-info commitów, ale nie usunął/dodał kodu — bo tego kodu w gicie nigdy nie było.

Dzisiejszy `fly deploy` z lokalnego master cofnął produkcję do stanu z kwietnia. Po wykryciu utraty zrobiliśmy rollback do image v273.

## Co odzyskano (na tym branchu)

### 1. Templates HTML/CSS/JS z binarki v273

Wyciągnięte przez `strings` + Python `re.findall` z `/tmp/humanmcp-v273` do `internal/web/templates_recovered/` (25 plików `.tmpl`):

- **Public pages:** `index.html`, `piece.html`, `images.html`, `listings.html`, `listing.html`, `artworks` (inferred), `team.html`, `personas.html`, `skills.html`, `questions.html`, `for-agents.html`, `subscribe.html`, `subscribe-confirm.html`, `connect.html`, `contact.html`, `login.html`
- **Owner UI:** `dashboard.html`, `mc.html` (Mission Control), `new.html`, `listing-new.html`, `llms-edit.html`, `messages.html`
- **Layout fragments:** `css`, `header`, `header-simple`, `footer`

### 2. Lista handlerów v273 (79 funkcji)

`~/Documents/humanmcp-recovery/v273-handlers.txt` — pełna mapa funkcji. Najważniejsze brakujące:

- `handleArtworks`, `handleArtworkDetail`
- `handleListings`, `handleListingRoute`, `handleListingNew`, `handleListingEdit`, `handleListingDelete`, `handleListingsFeed`
- `handleMissionControl` (`/mc`)
- `handleTeam`, `handleSkillsPage` (`/skills`), `handlePersonas` (uwaga: dziś już istnieje persona resource)
- `handleQuestions`, `handleAnswerQuestion`
- `handleForAgents`, `handleSubscribeForm`, `handleSubscribeConfirm`, `handleUnsubscribe`
- `handleRSS`, `handleAgentCard`, `handleLLMSTxt`, `handleLLMSTxtEdit`
- `handleProvenanceAdd/Delete/Edit`
- `handlePeerAdd/PeerRemove`
- `handleAPILinks`, `handleAPINotes`, `handleAPIPeers`, `handleAPIProfile`, `handleAPIProvenance`, `handleAPISearch`
- `handleKeyUpdate`, `handleSessionRotate`, `handleNewSessionTicket`
- `handleShort`, `handleTimestamp`, `handleHumansTxt`, `handleLICENSE`
- `handleOpenAPI`, `handleMetadata`

### 3. Lista MCP tools v273 (40 narzędzi)

`~/Documents/humanmcp-recovery/v273-mcp-tools.txt`

### 4. Persona Hodor + skille (wykonane dziś)

- `content/personas/hodor.md`
- `content/skills/operational-safety-public.json` (generic rules, public access)
- `content/skills/operational-safety-private.json` (historia incydentów Łukasza, gated)
- `content/skills/narada-wieczorna.json` (Pan Cogito ritual)

### 5. Hodor integration zmiany w `internal/mcp/handler.go`

- `handleInitialize` — sekcja "OPERATIONAL SAFETY" w instructions
- `toolBootstrapSession` — sekcja "GUARDIAN — LOAD FIRST" wczytywana zawsze pierwsza, plus skip-duplikatów w głównych listach
- `toolGetPersona` — wyjątek dla `hodor` (publicznie dostępny)
- `toolGetSkill` — wyjątek dla `-public` suffix (operational-safety-public publicznie dostępny)

### 6. Backup binarki

- `~/Documents/humanmcp-recovery/humanmcp-v273-binary` (8.5 MB) — pełna binarka do future decompile
- `~/Documents/humanmcp-recovery/v273-strings.txt` (102k linii) — surowe stringi
- `~/Documents/humanmcp-recovery/templates/*.tmpl` — 25 templates

## Co jeszcze zostało (rekonstrukcja w kolejnej sesji)

### Krok 2 — handlery Go (3-5h)

Dla każdego nowego templatu/route trzeba:
- Napisać funkcję `handleXXX` w `internal/web/handler.go`
- Zarejestrować route w `mux.HandleFunc` / `mux.Handle`
- Stworzyć potrzebne struct types (np. `Listing`, `Question`, `Subscription`, `Artwork`)
- Stworzyć storage layer (np. `internal/content/listings.go`, `internal/content/questions.go`)

Wskazówki:
- Template variables (`{{.Foo}}`) zdradzają kontrakt każdego handlera
- v273 binary + strings = źródło prawdy dla nazw, kolejności, brakujących endpointów
- Można decompile binarkę przez `ghidra` / `radare2 -A` / `goresym` jeśli sama analiza stringów za mało

### Krok 3 — Hodor integration

Już zrobione w gałęzi. Po rekonstrukcji handlerów wystarczy:
- Zachować zmiany z `internal/mcp/handler.go` (są tu)
- Upewnić się, że `narada-wieczorna`, `operational-safety-public/private` są publikowane

### Krok 4 — Build, test, deploy

- `go build ./...` — sprawdzić, że kompiluje
- `go test ./...` — testy
- `fly deploy -a kapoost-humanmcp` — wgrać nową wersję

**Uwaga:** przed deployem **commit i push do gita**. Lekcja z dziś: jeśli kod żyje tylko w binarce, jeden zły deploy go traci.

## Plan komitowania

Krok 4 musi mieć:
1. `git add` wszystkie pliki na branchu
2. `git commit -m "feat: recovery of v273 templates + Hodor integration"`
3. `git push -u origin hodor-recovery`
4. Po review w PR → merge do master
5. `fly deploy`

## Co działa TERAZ (po rollbacku)

- ✅ Produkcja: v273, wczorajsza UI (Mission Control, artworks, listings, keyboard shortcuts, dark/light, i18n, thumbnails)
- ✅ Dane na volume: pieces, persony, skille, wiadomości, questions, blobs
- ✅ Hodor i 3 skille na volume — widoczne po `bootstrap_session`
- ❌ Server instructions z OPERATIONAL SAFETY (nie ma w binarce v273, jest tylko w naszym brancu)
- ❌ bootstrap_session GUARDIAN block (jw.)
- ❌ get_persona/get_skill wyjątki dla Hodora (jw.)
