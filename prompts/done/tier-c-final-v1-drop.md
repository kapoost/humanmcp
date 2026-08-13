# Prompt: Tier C.d final — drop v1 MCP handler (-3600 LOC)

> Wklej w świeżą sesję (Plan mode w Claude Code, cwd = `/Users/kapoost/humanmcp-server`). Prompt samodzielny. Storyboardy 251/251 już przechodzą na v2 mount (commit `5e90a03`). Zostały 3 blocker steps: `parity_test.go` rewrite, `narada.go` extract do shared package, delete v1 handler.

## Cel

Skasować `internal/mcp/handler.go` + jego test suite. Netto **-3600 LOC**. Koniec „every feature costs 2x LOC because v1 + v2". Wave 3 diff kosztował +524 LOC (byłoby ~230 bez v1), wave 4 (horcrux, ewentualnie scoped write) mnożyłoby dalej.

## Kontekst (co JUŻ jest zamknięte)

**Zamknięte w Tier C.a-C.c (commits `05ae46e` + `7ed2c7b` + `5e90a03`, deploy v373-v374):**
- Storyboard runner mount v2 (`internal/storyboard/http.go`) — 251/251 pass
- `RenderServerInstructions` extracted do `internal/mcp/instructions.go` (public), v2 pass'uje przez `sdk.ServerOptions.Instructions` przy NewServer construction
- Session model: `internal/mcp/session.go` HMAC helpers + `Handler.IsSessionActiveByHeaders` dual-read (v1 Mcp-Session-Id map + v2 Bearer HMAC path) + v2 bootstrap emit `SESSION_TOKEN: <token>` preamble + storyboard runner auto-inject Bearer
- Transport-agnostic assertions (usunięte `"code":-32602` z YAMLów, zastąpione `"error"` presence check)

**Live na Fly v375:** wave 3 friend tokens + wave 2 write op + session model + ratelimit extract. Wszystkie zielone, deploy behavior-preserving.

## Blockery zostałe (twoje zadanie)

### 1. `narada.go` (785 LOC) do wyekstraktowania

`internal/mcp/narada.go` to async ritual pipeline — routing, LLM per-persona voice generation, journal management. **JEST v1-only**, nie ma odpowiednika w v2. Dwie opcje:

**Opcja A (rekomendowana): extract do `internal/rituals/`**
- Nowy pakiet `internal/rituals/` — RitualWorker struct z LLM client, StatStore, itd.
- v1 handler.go + v2/rituals.go oba `import "internal/rituals"` i wołają worker methods
- Po v1 drop, tylko v2/rituals.go zostaje jako caller

**Opcja B: duplikacja w v2/**
- Skopiować narada.go treść do v2/narada.go, adaptować do SDK signatures
- Podwaja LOC → celuje kontr Tier C misji
- **Nie wybieraj tej opcji.**

### 2. `internal/mcp/v2/parity_test.go` (419 LOC) do przepisania

Obecnie 82 test cases porównują v1 vs v2 byte-equal jako assurance że tools zwracają identycznie. Bez v1 → nie ma z czym porównać. Trzy opcje:

**Opcja A (rekomendowana): golden JSON fixtures**
- Wygeneruj snapshot każdego tool response z v2 (jednorazowo, commit jako fixture .json)
- Test loops przez fixtures, wywołuje v2 tool, porównuje z fixture
- Regression protection zachowany. Update fixture wymaga świadomej akceptacji zmiany.

**Opcja B: delete parity_test**
- Storyboardy pokrywają dużo behavior. Parity test to nadmiar.
- Ryzyko: subtelne response shape drift przechodzi niezauważony aż agent się zawiesi.
- **Wybieraj tylko jeśli storyboardy już pokrywają wszystkie 82 case'y** (weryfikuj przed decyzją).

**Opcja C: przekształć w unit tests per tool file**
- Każdy `internal/mcp/v2/*.go` dostaje `_test.go` z 1-3 basic assertions.
- Większy diff niż A, ale lepsza modularna organizacja.

### 3. Delete v1

Po (1) + (2):
- `internal/mcp/handler.go` (~2992 LOC minus RenderServerInstructions + narada wyekstraktowane, więc ~2000 LOC net)
- `internal/mcp/handler_test.go` (~493 LOC)
- `internal/mcp/mysloodsiewnia_tools.go` (~305 LOC — v2 ma to samo, safe)
- `internal/mcp/mysloodsiewnia_write_cap_test.go` (~stub, sprawdź czy testuje v1-only writeBodyCap albo shared const)

### 4. `cmd/server/main.go` — promote v2 do `/mcp`

Obecnie:
```go
mux.Handle("/mcp", corsMiddleware(mcpHandler))       // v1
mux.Handle("/mcp/v2", corsMiddleware(v2Handler))     // v2
mux.Handle("/mcp/v2/", corsMiddleware(v2Handler))
mux.Handle("/mcp/", corsMiddleware(mcpHandler))      // v1 catch-all
```

Po drop:
```go
mux.Handle("/mcp", corsMiddleware(v2Handler))
mux.Handle("/mcp/", corsMiddleware(v2Handler))
// /mcp/v2 alias opcjonalny — grep czy jakikolwiek klient go używa (docs/README/for-agents nie reklamują).
```

### 5. `internal/mcp/v2/handler.go` Source interface — promocja

Obecnie internal contract między v1 (implementer) i v2 (consumer). Po drop v1, Source staje się primary API surface `mcp` package. Rozważ:
- Rename `Source` → `Backend` lub `Store` (bardziej ekspresywne bez v1 context)
- Move do dedicated file `internal/mcp/backend.go`
- godoc opisujące że to jest THE contract dla wszystkich MCP callers

### 6. `internal/web/phantom_tools_test.go` — flip

Obecnie:
```go
h := mcp.NewHandler(cfg, nil, auth.New("test"))
out := map[string]bool{}
for _, n := range h.ToolNames() {
    out[n] = true
}
```

Po drop → v1 `mcp.NewHandler` nie istnieje. Zamień na v2 constructor. Ale v2 `New()` zwraca `http.Handler`, nie exposes ToolNames. Rozwiązanie: wyekstraktuj listę tool names do stałej `v2.ToolNames = []string{...}` albo helper.

### 7. Docs update

- `internal/mcp/v2/discovery.go` `about_humanmcp` tool — reklamuje endpoint. Grep czy mówi `/mcp` (v1 legacy path) czy generic. Update jeśli explicit.
- `README.md` — grep `/mcp/v2` references, może zastąpić przez `/mcp` (single endpoint po drop).
- `internal/web/templates/for-agents.html` + `connect.html` — same.
- `docs/index.html` — same.
- `.well-known/mcp-server.json` (`internal/web/handler.go:253`) — advertises endpoint, update.

### 8. Session model storyboard

Dodać `storyboards/mcp/session_token_bearer_flow.yaml` który explicit testuje new mechanism (Fly emit → runner extract → subsequent /mcp calls carry Bearer → members-tier visible). Obecnie session model side-effect'owo testowany przez `collection_access_gates_via_mcp` — dedicated storyboard = better regression signal.

## Kroki (nie improwizuj)

1. `git log --oneline --grep "Tier C" -10` — zorientuj się gdzie się skończyło.
2. Przeczytaj auto-memory: `~/.claude/projects/-Users-kapoost-humanmcp-server/memory/project_v1_drop_pending.md`.
3. Przeczytaj `internal/mcp/narada.go` w całości — planning extract to `internal/rituals/`.
4. Przeczytaj `internal/mcp/v2/parity_test.go` — planning golden fixtures.
5. **Odpal naradę PRZED restructure**: `run_narada(context="Tier C.d final v1 drop — narada.go extract options, parity_test fixture strategy, migration order")`. Router prawdopodobnie zbierze mira + maruda + ghost (+ może hermes na sekwencję). Wave 3 audit-driven sequences trafiły w 20s (`nar-2c9853dd5f68`), poprzednia 5-osobowa (`nar-9fc3e83b3e15`) się zaklinowała >60min — jak zombie znowu, przełącz na Plan B z audit rec bez czekania.
6. Po naradzie: ExitPlanMode → sekwencja implementacji.

## Sequence (post-narada, orientacyjnie)

1. Extract `narada.go` → `internal/rituals/` — commit A (behavior-preserving move, v1 i v2 oba używają nowego pakietu)
2. Verify all tests + storyboardy zielone
3. Rewrite `parity_test.go` na golden fixtures — commit B
4. Verify fixtures pokrywają 82 case'y ± dopisać brakujące storyboardy
5. Delete v1 files (`internal/mcp/handler.go`, `handler_test.go`, `mysloodsiewnia_tools.go`) — commit C (destructive)
6. Update `cmd/server/main.go` + Source interface promotion — commit D
7. Update `phantom_tools_test.go` + docs — commit E
8. Deploy → verify negative-path checks na prod
9. Update auto-memory: `project_v1_drop_pending.md` → mark as completed (delete file albo replace content z "shipped 2026-XX-XX")

**Effort estimate: 2-3 dni pracy jednej dedykowanej sesji.**

## Landminy

- **Nie start bez narady** (Maruda pin z audit): cleanup łatwo zamienia się w rewrite dla przyjemności. Narada broni przed scope creep.
- **Extract narada.go zachowaj behavior 1:1** — nie „przy okazji" refactor async loop, LLM client init, journal shape. Extract = mechanical move + import fix. Refactor osobny commit.
- **parity_test golden fixtures muszą pokrywać error paths** — nie tylko happy path. Wave 3 audit odkrył że parity_test testował tylko byte-equal na success, nie na error shape. Golden fixtures = szansa na naprawę.
- **`cmd/server/main.go` flip mount ostrożnie** — prod klienci uderzają `/mcp` (nie `/mcp/v2`). Deploy z v2 na `/mcp` = wszyscy klienci lądują na SDK Streamable HTTP. `Accept: application/json, text/event-stream` header wymóg — sprawdź czy real clients (Claude Desktop, Claude Code) to wysyłają domyślnie. Jak nie → dodać server-side header default albo tolerować missing.
- **Session model już wire'owany na v2** — po flip mount, storyboard runner logic z `sessionTokenRe` + auto-inject nie zmienia się. Ale REAL agents muszą zrozumieć że bootstrap zwraca SESSION_TOKEN i trzeba go wysłać. Bootstrap teach body już mówi ("Send on subsequent tool calls as Authorization: Bearer") — verify że real Claude Desktop / Claude Code parse'ują tę instrukcję poprawnie.
- **`.well-known/mcp-server.json`** — jak zmienisz endpoint mapping, sprawdź czy nadal poprawne (grep `mcp-server.json` w `internal/web/handler.go`).
- **Repo publiczne** — komentarz o SDK strip logic (obecnie w `internal/mcp/handler.go:232-240` po ostatniej edycji) usunięty, dobrze. Nowy kod nie powinien tłumaczyć „why SDK quirks", tylko „what invariant".

## Deliverables (checklist)

- [ ] Narada odbyty, wnioski udokumentowane (commit tag `[narada:<id>]` na kolejnych commitach)
- [ ] `internal/rituals/` pakiet utworzony, `narada.go` moved out z v1
- [ ] v1 + v2 oba używają `internal/rituals/` — build + testy zielone przed dalszymi zmianami
- [ ] `internal/mcp/v2/parity_test.go` przepisany na golden fixtures LUB deleted po weryfikacji storyboards coverage
- [ ] `internal/mcp/handler.go` + `handler_test.go` + `mysloodsiewnia_tools.go` deleted (rough -3200 LOC)
- [ ] `cmd/server/main.go` — v2 na `/mcp`, v1 wire out, log line aktualizowany
- [ ] `internal/mcp/v2/handler.go` Source interface — considered rename + move do dedicated file
- [ ] `internal/web/phantom_tools_test.go` — flip z v1 na v2 constructor
- [ ] Docs updated: `about_humanmcp`, README, for-agents.html, docs/index.html, `.well-known/mcp-server.json`
- [ ] Nowy storyboard `mcp/session_token_bearer_flow.yaml` — dedicated regression signal
- [ ] `go test -race ./... -count=1` zielone
- [ ] `flyctl deploy` + prod negative-path smoke (anonymous → Unauthorized, malformed Bearer → Unauthorized, invalid session token → not-active for members)
- [ ] Auto-memory `project_v1_drop_pending.md` zastąpiony przez `project_v1_dropped.md` z faktyczną datą i commit hashami
- [ ] `docs/adr/0001-mysloodsiewnia-bridge.md` addendum: "2026-XX-XX — Tier C final v1 drop, -3600 LOC"

## Referencje

- Auto-memory `project_v1_drop_pending.md` — pełna roadmapa
- Auto-memory `project_wave3_wave2_shipped.md` — kontekst poprzedniego stanu (Wave 3 + Wave 2 shipped, session model wired)
- Commits: `05ae46e` (Tier C.a+c), `7ed2c7b` (session foundation), `5e90a03` (bootstrap wire + runner flip)
- Wave 3 audit (nar-67cdd80179c2, 2026-08-12) — original identyfikacja v1/v2 duplication
- Session model narada (nar-2c9853dd5f68, 2026-08-12) — Bearer session_token design decision
- `feedback_batch_edit_read_first.md` — landmine przy batchach Editów na plikach jeszcze nie Read'owanych
