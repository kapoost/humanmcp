# Prompt: humanMCP ↔ mysłoodsiewnia bridge (read+write, gated by home-live)

> Prompt do wklejenia w świeżą sesję (Plan / general-purpose agent) — brief samodzielny, nie zakłada pamięci poprzednich sesji.

## Cel

Rozszerz humanMCP (`kapoost-humanmcp` na Fly) o zestaw narzędzi MCP dających świeżemu agentowi dostęp read+write do zasobów mysłoodsiewni (domowy serwer kapoosta): kopie maili, dokumenty, później więcej. Narzędzia są aktywne **tylko gdy mysłoodsiewnia jest live** — w przeciwnym razie zwracają czytelny „offline" komunikat, nie 500.

## Kontekst (co już jest)

- humanMCP: Go server, MCP v2 handler na go-sdk v1.7.0 (patrz `internal/mcp/v2/`), obecnie 36 toolów. Public repo `kapoost/humanmcp`, deploy `flyctl deploy --app kapoost-humanmcp`.
- Skille (`content/skills/*.json`), personas, storyboardy (`storyboards/`) — dyscyplina SDD opisana w `content/skills/storyboard-driven-development.json`. Nowy tool = nowy storyboard `kind: http` z POST na `/mcp`.
- Istniejący kanał **z mysłoodsiewni do humanmcp**: `POST /sync-humanmcp` na porcie 7331 pushuje stan. Kierunek odwrotny (humanmcp → mysłoodsiewnia) nie istnieje — trzeba go zaprojektować.
- Auth wzorce w kodzie: `EditToken` (owner), `AgentToken` (agent), `SessionSecret` (bootstrap flow). Owner-only toole wymagają `Authorization: Bearer <edit token>`.
- Hodor persona + operational-safety skill: żadnych sekretów w logach, `.gitignore` chroni `.env`/`config.json`/`.mcp.json`, repo jest public więc każdy commit z sekretem = permanentny leak.

## Twarde wymagania

1. **Liveness gate**: każdy tool sprawdza świeżość heartbeatu mysłoodsiewni przed próbą operacji. TTL: propozycja 30s. Bez świeżego heartbeatu → tool zwraca `{"status":"offline","last_seen":"…"}` z HTTP 200 (to nie błąd — to stan). Nigdy nie zawieszaj toolu na network timeout wobec mysłoodsiewni.
2. **Owner-only**: read i write są owner-only (`Authorization: Bearer <edit token>`). Bez tokena → `Unauthorized`, tak jak w istniejących editing toolach.
3. **Zero sekretów w git**: klucze/tunnele/hasła siedzą w `flyctl secrets`, nigdy w repo.
4. **Storyboardy jako gate**: każdy nowy tool ma storyboard w `storyboards/mysloodsiewnia/` z asercją online + offline path. Sabotage-test (wywal liveness check → asercja offline musi failować).
5. **Nowy tool w 7 miejscach**: patrz feedback `mc counter wire pattern` — pomiń jedno miejsce i tool jest phantomem lub cichnie.
6. **Bootstrap teach**: `bootstrap_session` musi wspomnieć nową rodzinę toolów (`mysloodsiewnia_*`) i warunek offline, inaczej agent ich nie użyje.

## Decyzje do podjęcia (nie zgadywać — spytać kapoosta)

**D1. Reachability Fly → dom.** Fly jest public, mysłoodsiewnia za NAT-em. Opcje:
- (a) **Pull model**: mysłoodsiewnia poll'uje humanmcp o wiszące operacje (`GET /mysloodsiewnia/pending-ops`), wykonuje, POSTuje wynik. Prosty, żadnego tunelu, ale każda operacja to round-trip przez kolejkę.
- (b) **Cloudflare Tunnel / Tailscale / Wireguard mesh**: Fly machine → mysłoodsiewnia bezpośrednio przez tunel. Real-time, ale wymaga trzymania sekretu tunelu na Fly + operational overhead.
- (c) **Reverse tunnel z domu (SSH -R, chisel, frp)**: mysłoodsiewnia otwiera połączenie do Fly, humanmcp callback'uje przez ten socket.

Rekomendacja domyślna: **(a) pull model** — pasuje do heartbeatu, brak sekretu do wycieku, offline fallback trivialny (pusta kolejka wygasa, agent widzi offline). Trade-off: kilkusekundowa latencja na operację.

**D2. Storage na mysłoodsiewni.** Gdzie i w jakim formacie leżą maile/dokumenty? Istniejąca struktura czy zaprojektujemy? Format (mbox / eml / sqlite / plain md)?

**D3. API surface — pierwsza fala toolów.** Propozycja:
- `mysloodsiewnia_status` — liveness + statystyki (ile maili, ile dokumentów, ostatni heartbeat)
- `mysloodsiewnia_search` — full-text search po korpusie (query, filters, limit)
- `mysloodsiewnia_get` — pobierz konkretny zasób po ID/ścieżce
- `mysloodsiewnia_list` — przeglądaj (folder/tag/date range)
- `mysloodsiewnia_write` — zapisz nowy dokument (typ, treść, metadata)

Cięcie: czy `write` jest w tej fali czy odłożone do sesji 2 po sprawdzeniu read-only na produkcji?

**D4. Auth model mysłoodsiewnia ↔ Fly.** Shared secret w `flyctl secrets` + weryfikacja HMAC na body? mTLS? Jeśli pull model — shared secret na endpointach `/mysloodsiewnia/*` po stronie Fly wystarczy.

**D5. Rate limits + safety.** Czy write ma cap (bytes/min)? Czy delete jest w scope? Jeśli nie — powiedz to explicit w opisie toolów.

## Deliverables

1. Design doc (ADR) w `docs/adr/NNNN-mysloodsiewnia-bridge.md` z wybraną odpowiedzią na D1–D5.
2. Nowe toole MCP w `internal/mcp/v2/tools/mysloodsiewnia_*.go` + rejestracja w handlerze.
3. Storyboardy w `storyboards/mysloodsiewnia/*.yaml` — po jednym per tool, każdy z online + offline path + sabotage-verified.
4. Aktualizacja `bootstrap_session` body — wzmiankuj rodzinę i offline semantykę.
5. Aktualizacja `docs/index.html` (twitter card + feature card) — nowa liczba toolów, żeby `TestDocsToolCountMatchesReality` przeszedł.
6. Aktualizacja skilla `storyboard-driven-development.json` — jeśli dochodzą nowe reguły klasy.
7. Po stronie mysłoodsiewni: klient (heartbeat + response) — osobny commit w repo mysłoodsiewni.

## Landminy (patrz memory)

- **Dual parser**: humanmcp ma dwa parsery persony (MCP + web). Nowe pola na Blob/Resource lądują w OBU parserach, inaczej dashboard cicho gubi wartość. Sprawdź czy dotyczy też nowych resource types.
- **Fly volume orphans**: nie upsertuj skilli bezpośrednio na `/data/` bez git counterparta.
- **MCP responses = agent prompt**: body toolów kształtuje każdą świeżą sesję. Żadnych hardcoded liczb, żadnych halucynacyjnych obietnic. Pin z `body_does_not_contain`.
- **Hodor first responder**: implementacja z sekretami/tunelem = automatyczny load Hodor. Weryfikuj z użytkownikiem przed przechowaniem czegokolwiek wrażliwego.
- **Prod verify hygiene**: negatywne ścieżki (offline, unauthorized) na live; pozytywne (write happy path) TYLKO na storyboardach, nie na prod, żeby nie zaśmiecać korpusu.

## Checklista przed pierwszym commitem

- [ ] D1–D5 rozstrzygnięte i zapisane w ADR
- [ ] Każdy tool ma storyboard z sabotage-verified asercją (online + offline)
- [ ] `go test -race ./...` zielone (włącznie z `TestDocsToolCountMatchesReality`, `TestNoPhantomToolsInAgentDocs`)
- [ ] Bootstrap body wzmiankuje nowe toole
- [ ] `flyctl secrets list` zawiera potrzebne klucze; nic sekretowego w git
- [ ] Client po stronie mysłoodsiewni committed osobno + uruchomiony w domu
