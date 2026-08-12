# Prompt: mysłoodsiewnia-bridge wave 3 — sharing / friend tokens

> Prompt do wklejenia w świeżą sesję (Plan / general-purpose agent) — brief samodzielny, nie zakłada pamięci poprzednich sesji. Design zamknięty naradą `nar-67cdd80179c2` (5/5 konsensus), ADR-0001 sekcja "Wave 3 — sharing / friend tokens (design accepted 2026-08-12)". Commit tag: `[narada:nar-67cdd80179c2]`.

## Cel

Rozszerz istniejący bridge humanMCP ↔ mysłoodsiewnia o **friend tokens** — per-recipient tokeny z wąskim, statycznie audytowalnym scope'em, tak żeby kapoost mógł dawać znajomym read-only dostęp do wybranej części vaultu (np. tylko `literatura`+`note`, żadnych faktur, żadnych `access:private`). Dziś bridge jest single-tenant: jeden `EditToken` bramkuje wszystkie `mysloodsiewnia_*` toole. Wave 3 to zamiana `IsOwnerRequestByHeaders` → `IsAuthorizedRequestByHeaders(scopes)` na całej ścieżce Fly→queue→vault.

**Read-only.** Write (np. `leave_comment` dla scoped tokena) to osobny ADR po 30 dniach obserwacji wave 3 w prod.

## Kontekst (co już jest)

- Bridge wave 1 + 1.5 live: `mysloodsiewnia_status`, `_search`, `_get`, `_list` (owner-only, `Authorization: Bearer $EditToken`). Pull model przez queue (`GET /mysloodsiewnia/pending-ops` + `POST /mysloodsiewnia/complete`). Auth vault↔Fly: shared `VAULT_BRIDGE_TOKEN` w Fly secrets + `~/mysloodsiewnia/config/bridge.env` chmod 600.
- Liveness gate (30s TTL) — tool bez świeżego heartbeatu vaultu zwraca `{status:"offline"}` z HTTP 200, nigdy nie zawiesza się na network timeout.
- **Scaffold już istnieje po stronie vaultu**: `content.access` field na dokumentach (`private`/`public`/`friends`) + `tokens.json` (chmod 600, gitignored — linia 14 `.gitignore`, nigdy nie w historii, potwierdzone `git log --all -S FRIEND_TOKEN` = 0).
- SDD dyscyplina: nowy tool = nowy storyboard `kind: http` z asercją online + offline path + **sabotage-verified** (wywal gate → offline asercja musi failować, patrz `storyboards/mysloodsiewnia/SABOTAGE_LOG.md`).
- Repo humanmcp jest **publiczne od 2026-08-05**. Cała historia commitów wave 3 będzie widoczna od dnia mergu — implikacje w sekcji Landminy.
- Hodor persona + operational-safety skill: żadnych sekretów w logach ani terminalu (pipe do `pbcopy` albo `$(…)`).

## Chosen design (od narady, do implementacji 1:1)

### W1 — Scope grammar: static enum, nie DSL

Scope to `doc_type IN (<enum values>)` gdzie enum jest statycznie zdefiniowanym setem: `literatura`, `note`, `pdf`, itd. **Zero JSONata / CEL / expression evaluatorów** w authorization path — narada 5/5 wprost odrzuciła expressive grammar jako CVE-czekające-na-datę. Nowy use case = nowy commit z enum extension, nie nowy filtr od użytkownika.

**Contrarian addendum (Z1)**: kapoost jest piszącym poetą — nowe `doc_type` będą się pojawiać (żegluga-log, nowe gatunki). Wave 3 idzie z **positive-list only** (bezpieczniejsze), ale wariant **wildcard-minus-exclusion** (`scopes: ["*"], exclude: ["private"]`) jest udokumentowany jako deferred, nie odrzucony. Rewizja po 30 dniach live jeśli rotation przy nowych typach będzie operacyjnym bólem. Nie implementuj wildcard w wave 3 bez potwierdzenia kapoosta.

Format `tokens.json` (vault-side, po Fly-side lookup):

```json
{
  "slug_A": {
    "scopes": ["literatura", "note"],
    "rate_limit_per_hour": 50,
    "expires_at": "2026-11-30T23:59:59Z",
    "created_at": "2026-08-15T10:00:00Z"
  }
}
```

`slug_A`, `slug_B` w fixture'ach — **nigdy realnych imion w git** (patrz Landminy).

### W2 — Audit: per-response, transactional, na vault-side

Rozszerz `stats.ndjson` (lub jego następcę) o transakcyjny wpis per call:

```
{"ts":"2026-08-…","token_id":"slug_A","tool":"mysloodsiewnia_search","doc_ids":["hash1","hash2",…],"count":23}
```

Hash contentu, nie content sam. **Request-only logging = security theatre** — przy podejrzeniu leaka o 3am wpis "Alice pytała o literaturę" bez "Alice dostała te 23 slugi" jest zerowej wartości forensicznej.

**Krytyczne (Mira)**: transakcyjny wpis pisze się **na vault-side, po SQL filter**, w tym samym request-response cycle. Nie rozdzielaj request-side na Fly + response-side na vault jako dwa logi do korelacji później — to jest ból na incident time.

### W3 — `access:private` całkowicie niewidoczne

Scoped token widzi vault jakby `access:private` dokumenty nie istniały. **Zero rows** w wynikach, zero increment w count, zero mention w listingu (`mysloodsiewnia_list`). Metadata leak (`"document exists but you can't see it"`) na 9k dokumentach + 50 req/hr = grafowa inferencja prywatnej struktury vaultu w tygodniach.

Implementacja: vault SQL layer dodaje `AND access != 'private'` dla scoped callerów **przed** każdą operacją COUNT / LIMIT / listing, nie po. Nie ma "denied", jest "not found" — semantycznie identyczne dla scoped tokena z sytuacją kiedy dokument faktycznie nie istnieje.

**KRYTYCZNY landmine (Z2)**: ta semantyka jest **celowo asymetryczna** z out-of-scope response. Out-of-scope `doc_type` → HTTP 403 z informacją `{"allowed":[…]}` (edukujemy frienda o jego scope). `access:private` match → zero rows, bez sygnału że coś istnieje (privacy by erasure). Implementator który czyta tylko kod i zobaczy "403 tu, silent skip tam" **będzie chciał to ujednolicić — nie ujednolicaj**. Scope filter to scope education, privacy filter to privacy by erasure. Różne threat models, różne odpowiedzi. Ta uwaga jest w ADR z tego samego powodu.

### W4 — Revocation: immediate hard cut, deployment atomicity load-bearing

`flyctl secrets unset FRIEND_TOKEN_<slug>` → Fly restart → token martwy. **Zero TTL cache po stronie vaultu** który przeżyje token po Fly revoke. Yuki dopuścił 60s cache tylko z aktywnym alertem "token X używany <cache_ttl>s po revoke" — kapoost takiego alertu nie ma, więc cache = nadzieja.

**Sync tokens.json Fly ↔ vault (Mira, load-bearing)**: scope grammar żyje w trzech miejscach — Fly (token → scopes), bridge queue (propagate), vault (SQL filter + audit). Vault **nie polluje** `tokens.json` na interwale. Dwa akceptowalne mechanizmy:

- **Webhook** z Fly na vault przy zmianie sekretu (`POST /reload-tokens`, wymaga endpointa na vault-side + shared secret).
- **SIGHUP** od operatora vaultu (kapoost robi manualny push po `flyctl secrets set`).

Poll model tu jest wprost zły — "revoked in Fly, honored in vault" to jest *the* case przy skradzionym laptopie frienda, nie edge case.

**Belt & suspenders**: `expires_at` zakodowane w samym tokenie (payload), nie tylko w lookup table. Jeśli lookup zawiedzie (sync race), token nadal wygasa sam z siebie.

**Pinned invariant (Z3)**: revocation soundness zależy od invariantu że **vault nie ma direct public endpoint** — Fly jest jedynym ingressem. Jeśli kapoost robi `secrets unset` gdy vault jest offline (łódka, power cut), token żyje w pamięci vaultu ale żaden request go nie dosięgnie (Fly refuse'uje na boundary). Bezpieczne *tylko* pod obecną architekturą. Jeśli vault kiedyś dostanie direct endpoint (ngrok, Tailscale wystawiony na public, wave 4+), revocation logic wymaga review **przed** exposure. To nie jest implementation detail — to load-bearing invariant.

### W5 — Read-only. Write nie w wave 3.

Żadnego `leave_comment_scoped`, żadnego `write_document_scoped` w tej fali. Wave 2 (owner write) nawet nie istnieje jeszcze w prod — scoped write na nieistniejącej bazie to inverted order. Sequence: wave 3 read → 30 dni obserwacji → osobny ADR + narada na scoped write.

### Horcrux vs sharing token (Z6 — nie mieszaj)

Contrarian pass wskazał że **horcrux use case** (`project_horcrux` w memory kapoosta — dostęp do vaultu dla zaufanego powiernika na wypadek gdyby coś kapoostowi się stało) ma inne wymagania operacyjne niż sharing token. Token z `expires_at: 2026-11-30` jest **bezużyteczny jako horcrux** — renewal wymaga aktywnego udziału kapoosta, a to jest dokładnie ten stan który horcrux ma przetrwać.

**Wave 3 nie implementuje horcrux tokenów.** Mechanizm jest ten sam (`tokens.json` shape, scope grammar, audit), ale dyscyplina operacyjna różni się ostro:

- **Sharing token**: `expires_at` 30-90 dni, rotacja na scheduled, wąski scope.
- **Horcrux token**: długi lub open-ended TTL, procedura renewal bez interakcji kapoosta (dead-man switch? auto-renew na absence heartbeat? — osobny ADR).

**Nie twórz żadnego tokena z `expires_at > 1 rok` ani bez `expires_at`** w wave 3. Osobna decyzja (prawdopodobnie druga narada) przed pierwszym horcrux tokenem, bo threat model zmienia się znowu (recipient może nie wiedzieć że ma token do momentu trigger event).

### Prerequisite: rate limit per token — per-token config, nie hardcoded default

**Nie opcja, prerequisite.** Bez per-token cap friend token to bulk-dump narzędzie w friendly hat. Enforcement:

- **Vault-side** (last line of defence): sliding window per `token_id`, odrzuca z HTTP 429 przed SQL query.
- **Fly-side** (fast path): mirror counter, żeby nie płacić round-tripa do vaultu na abuse.

Counter per-token, nie shared. EditToken bypass'uje cap (owner robi co chce).

**Contrarian addendum (Z4)**: **żadnego hardcoded universal default** w kodzie. Narada nie uzasadniła konkretnej liczby, a jedna wartość jest albo za hojna dla ochrony przed bulk dumpem, albo za tight dla legitymnego skryptu frienda-programisty. Source of truth to `tokens.json` (`rate_limit_per_hour` per token). Fallback jeśli field brakuje: **30 req/hr** (konserwatywnie — łatwiej rozluźnić per-token niż zaostrzyć). Rewizja po 30 dniach live.

## Twarde wymagania (nienegocjowalne)

1. **Nie łam wave 1 auth**: `EditToken` musi nadal działać bez zmiany, jako "owner scope = unlimited". `IsOwnerRequestByHeaders(r)` zostaje jako compatibility shim wołany przez `IsAuthorizedRequestByHeaders(r, scopes)` kiedy scope nie jest wymagany.
2. **Storyboardy jako gate** — minimum cztery nowe w `storyboards/mysloodsiewnia/wave3_*.yaml`:
   - `scoped_read_positive` — token slug_A z scope `[literatura]` czyta doc `doc_type=literatura` — OK.
   - `scoped_read_out_of_scope` — ten sam token pyta o `doc_type=pdf` — musi zwrócić 403 lub pusty result (zdecyduj i pin w storyboardzie).
   - `access_private_invisible` — scoped token woła `_list` — result nie zawiera *żadnych* rekordów z `access:private`, count reflektuje filtr, nie total.
   - `revoked_token_hard_cut` — token odwołany, wywołanie po Fly restart daje unauthorized; wywołanie w 60s oknie po `secrets unset` też daje unauthorized (nie honoruj cache).
3. **Każdy storyboard sabotage-verified** — wywal auth check → asercja musi failować. Zapis do `SABOTAGE_LOG.md`.
4. **Bootstrap teach**: `bootstrap_session` response musi wzmiankować że są friend tokeny i że owner-only toole nadal wymagają `EditToken`. Bez tego świeży agent nie wie o modelu.
5. **`docs/index.html`** — jeśli zmieniasz tool count albo dodajesz nowe toole (nie musisz — friend tokens to auth layer, nie nowe toole), sync feature card + twitter card, żeby `TestDocsToolCountMatchesReality` przeszedł.
6. **Nowy tool wire pattern (jeśli dodajesz)**: 7 miejsc do skoordynowanego touch (patrz memory `mc counter wire pattern` + `internal/web/phantom_tools_test.go`).
7. **Sekrety w `flyctl secrets` only**, nigdy w git. Fixture'y w testach używają `slug_A`, `slug_B`, `test_token_do_not_use_in_prod`.

## Landminy (patrz memory + wave 1 doświadczenia)

- **Repo public od 2026-08-05** (`project_repo_public.md`): każdy commit wave 3 jest permanentnie indeksowany. Realne slug names znajomych + realne scope patterns identyfikujące dostęp = leak nawet po `git rm`. **Fixture'y ANONIMOWE, always.** `slug_A` nie `alicja`, `[type_x, type_y]` nie `[fotografia_analogowa, notes_terapia]`.
- **Dual parser landmine** (`feedback_dual_parser_landmine`): humanmcp ma dwa parsery persony (MCP + web). Jeśli dodajesz nowe pola do jakiegokolwiek shared resource (np. `Blob.access`, `Content.scopes`), musisz dotknąć **obu** parserów, inaczej web dashboard cicho gubi wartość.
- **Fly volume orphans** (`feedback_fly_volume_orphans`): nie upsertuj żadnych tokenów ani plików skilli bezpośrednio na `/data/` bez git counterparta. `entrypoint.sh` force-refreshuje tylko bundled `/app/default-content` — orphans na volumie żyją wiecznie.
- **MCP responses = agent prompt** (`feedback_mcp_responses_are_agent_prompts`): body toola shape'uje każdą świeżą sesję. Zero hardcoded liczb (ile tokenów aktywnych — dynamic z lookup), zero halucynacyjnych obietnic ("zawsze zwróci 50 wyników" — powiedz "do 50"). Pin body z `body_does_not_contain` na słowa-pułapki.
- **Hodor first responder** (`feedback_hodor_first_responder`): implementacja z sekretami/tunelem = auto-load Hodor + operational-safety skill przed pierwszym touch'em na `flyctl secrets`. Weryfikuj interaktywnie zanim cokolwiek wrażliwego trafi do `secrets set`.
- **Prod verify hygiene** (`feedback_prod_verify_hygiene`): negatywne ścieżki (out-of-scope 403, revoked token, unauthorized) — na live. Pozytywne (happy path scoped read) — TYLKO na storyboardach, nie na prod, żeby nie zaśmiecać `stats.ndjson` corpus.
- **Never print secrets to terminal** (`feedback_never_print_secrets`): nawet dla "verification". Pipe do `pbcopy`, użyj w `$(…)`, waliduj przez hash-compare, nie print.
- **Sync mysłoodsiewnia → humanmcp-server** (`reference_mysloodsiewnia_sync`): jeśli zmiana w tokens grammar wpływa na `POST /sync-humanmcp` (port 7331), pamiętaj że `entrypoint.sh` force-refreshuje personas z bundled `/app/default-content` — nie polegaj na stanie z runtime.

## Deliverables

1. **Kod bridge (Fly side)** w `internal/mcp/v2/tools/mysloodsiewnia_*.go`:
   - `IsAuthorizedRequestByHeaders(r, requiredScopes) (tokenID, granted, ok)` — nowy shim, zwraca też `tokenID` do propagacji do queue.
   - `EditToken` path traktowany jako `scopes=["*"]`, `tokenID="owner"`.
   - Friend tokens loadowane z env (`FRIEND_TOKEN_<slug>=<random_hex_32>`) na start + reload na sygnał (SIGHUP handler).
   - Rate limit counter per `tokenID` (sliding 1h window, in-memory z persist na Fly-side do vault po restart).
2. **Kod vault (osobny commit w repo mysłoodsiewnia)**:
   - `services/humanmcp_bridge.py` — akceptuje `token_id` + `scopes` w queue payload; enforce'uje `AND access != 'private'` + `AND doc_type IN scopes` na wszystkich SELECT'ach dla non-owner.
   - `stats.ndjson` writer — transakcyjny wpis `{ts, token_id, tool, doc_ids, count}`.
   - `tokens.json` reload przez SIGHUP + webhook endpoint `POST /reload-tokens` (chmod'ed shared secret z Fly).
3. **Storyboardy** — cztery z sekcji Twarde wymagania + sabotage verify.
4. **`storyboards/mysloodsiewnia/SABOTAGE_LOG.md`** — dopisz wpis per storyboard.
5. **Bootstrap body update** — wzmiankuj friend token model, offline semantykę bez zmiany.
6. **Aktualizacja ADR-0001 addendum** (nie nowy ADR) — po implementacji dopisz "2026-XX-XX — wave 3 wired up, storyboardy zielone, deployed to Fly".
7. **Rotation runbook** — dopisz do `~/Documents/humanmcp-incident-playbook.txt` (patrz memory `project_humanmcp_deployment`) sekcję: jak dodać nowego friend tokena, jak odwołać, jak sprawdzić audit.

## Decyzje otwarte (spytaj kapoosta zanim zaimplementujesz)

- **Out-of-scope response**: HTTP 403 z ciałem `{"status":"out_of_scope","allowed":["literatura","note"]}` czy pusty result z HTTP 200 `{"results":[],"count":0}` (semantyka jak `access:private`)? Trade-off: pierwsza opcja uczciwa dla frienda, druga zero-leak. Rekomendacja: 403 (frienda uczymy o jego scope, nie ukrywamy istnienia `doc_type`).
- **Webhook vs SIGHUP dla token reload**: webhook wymaga endpointa vault-side + shared secret; SIGHUP wymaga że kapoost manualnie signaluje po `secrets set`. Rekomendacja start: SIGHUP (mniej ruchomych części), webhook w wave 3.5 jak procedura zrobi się częsta.
- **Fixture strategy**: `tokens.json.example` w git z `slug_A`/`slug_B` czy zero fixture'ów i tylko dokumentacja formatu? Rekomendacja: `example` z jawnym `# nie używaj w prod, przykład formatu`.
- **Rate limit persist**: reset przy Fly restart (in-memory) OK, czy potrzebny persist do vault żeby restart nie był bypass'em? Rekomendacja: in-memory dla wave 3, upgrade jeśli abuse zaobserwowany.

## Checklista przed pierwszym commitem

- [ ] Cztery storyboardy zielone + wszystkie sabotage-verified (SABOTAGE_LOG.md wpisy)
- [ ] `go test -race ./...` zielone (włącznie z `TestDocsToolCountMatchesReality`, `TestNoPhantomToolsInAgentDocs`, `TestBootstrapMentionsAllToolFamilies`)
- [ ] `git log --all -S "friend"` i `-S "FRIEND_TOKEN"` na obu repo — zero realnych imion / patternów
- [ ] `flyctl secrets list` zawiera `FRIEND_TOKEN_slug_A` (przykładowy) — realne slugs dodawane osobno, nie w code review PR
- [ ] Bootstrap body wzmiankuje friend model
- [ ] Vault client committed osobno w repo mysłoodsiewnia + uruchomiony w domu (`SIGHUP` handler żywy)
- [ ] Rotation runbook w `humanmcp-incident-playbook.txt` zaktualizowany
- [ ] Contrarian sanity pass odbyty — 5/5 konsensus narady warty devil's advocate lap (zrobione 2026-08-12, wyniki wbudowane w W1/W3/W4/Prerequisite/Horcrux sekcje)
- [ ] Nie tworzysz tokena z `expires_at > 1 rok` ani open-ended TTL (patrz Horcrux vs sharing — osobna decyzja)
- [ ] `access:private` zero-rows response NIE ujednolicony z out-of-scope 403 (asymetria celowa, patrz W3 landmine)
- [ ] Commit subject/body zawiera `[narada:nar-67cdd80179c2]`

## Referencje

- ADR: `docs/adr/0001-mysloodsiewnia-bridge.md`, sekcja "Wave 3 — sharing / friend tokens (design accepted 2026-08-12)"
- Narada log: `nar-67cdd80179c2` (fetch przez `mcp__claude_ai_humanMCP__fetch_narada_result`)
- Wave 1 brief (mirror struktury): `prompts/mysloodsiewnia-bridge.md`
- Wire pattern audit: `internal/web/phantom_tools_test.go`
- Skill: `content/skills/storyboard-driven-development.json`
- Skill: `content/skills/operational-safety-public.json` (Hodor)
