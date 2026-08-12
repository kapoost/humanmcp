# Kickoff: wave 3 mysłoodsiewnia-bridge implementation

> Wklej ten prompt w świeżą sesję (Plan mode w Claude Code, cwd = `/Users/kapoost/humanmcp-server`). Prompt samodzielny — nie zakłada pamięci wcześniejszych sesji. Design został zamknięty naradą `nar-67cdd80179c2` (5/5) + contrarian pass (Z1-Z4+Z6). Ten prompt uruchamia implementację, nie design.

## Co ma się stać w tej sesji

Zaimplementuj wave 3 sharing/friend tokens po **obu stronach**:

1. **Fly side (ten repo, Go)** — auth layer, scope enforcement, rate limiter, bootstrap teach.
2. **Vault side (`~/mysloodsiewnia`, osobne repo, Python/FastAPI)** — SQL filter dla scoped callerów, transakcyjny audit z doc_id hashami, SIGHUP handler dla `tokens.json` reload.

Kolejność bezwarunkowa: **storyboardy RED zanim jedna linia kodu**, sabotage-verify po każdej zielonej asercji, dopiero potem prod deploy.

## Kanony do przeczytania NA POCZĄTKU (nie zgaduj, przeczytaj)

W tej kolejności:

1. **`docs/adr/0001-mysloodsiewnia-bridge.md`** — cały ADR, w szczególności sekcja "Wave 3 — sharing / friend tokens (design accepted 2026-08-12)" (linie ~132-260). Każda decyzja W1-W5, Prerequisite, Horcrux subsection, contrarian addenda (Z1/Z2/Z3/Z4/Z6). Sekcja Rollback plan + Sabotage-verification protocol na końcu.

2. **`prompts/wave3-sharing.md`** — brief napisany specjalnie do tej sesji. Zawiera chosen design, twarde wymagania, landminy, deliverables po obu stronach, checklista przed pierwszym commitem. **Ten dokument jest twoją specyfikacją** — jeśli coś jest niejasne, wracaj tutaj.

3. **`prompts/wave3-storyboards/README.md`** + cztery YAML w tym katalogu — executable acceptance tests. Nie są jeszcze w `storyboards/mysloodsiewnia/` bo runner nie wspiera friend tokenów. Twoje zadanie: rozszerz runner, potem `mv` YAML, potem uruchom przeciw impl.

4. **`prompts/mysloodsiewnia-bridge.md`** — brief wave 1 (już zaimplementowany, live). Pokazuje jak wygląda gotowy brief w tym projekcie i konwencje SDD.

5. **`storyboards/mysloodsiewnia/*.yaml`** (cztery pliki wave 1 + SABOTAGE_LOG.md) — kanon storyboardów w tym repo. Jak wyglądają zielone storyboardy dla wave 1 + jak wygląda wpis sabotage-verify.

6. **`internal/mcp/v2/tools/mysloodsiewnia_*.go`** — kod wave 1. Wave 3 wpina się w te same tools przez rozszerzenie auth check'u, nie przez nowe tools.

7. **`internal/auth/`** — `IsOwnerRequestByHeaders`. To ta funkcja się rozrasta w `IsAuthorizedRequestByHeaders(r, requiredScopes) (tokenID, granted, ok)`.

8. **`internal/storyboard/http.go`** — runner. Musi umieć załadować friend tokens z `cfg.FriendTokens` (nowe pole w `internal/config/`).

Po vault-side (osobny repo `~/mysloodsiewnia`):

9. **`services/humanmcp_bridge.py`** — obecny worker queue. Wpina się scoped SQL filter + audit writer.
10. **`~/mysloodsiewnia/tokens.json`** (chmod 600, gitignored) — format już ustalony w brief W1. NIE dodawaj do git nawet accidentally.

## Historia decyzyjna (git)

Wszystkie commity wave 3 tagged `[narada:nar-67cdd80179c2]`. Do prześledzenia decyzji:

```
git log --grep='narada:nar-67cdd80179c2' --reverse
```

Powinno pokazać cztery commity: ADR design accepted, brief, contrarian pass, storyboardy. Jeśli tego nie widać — coś jest nie tak, zapytaj kapoosta.

## Pierwsze pięć akcji (nie improwizuj)

1. Uruchom `git log --grep='narada:nar-67cdd80179c2' --reverse --stat` — zorientuj się co już jest w repo.
2. Przeczytaj `prompts/wave3-sharing.md` w całości. Nie skimuj — landminy w środku są krytyczne.
3. Uruchom `go test ./... -count=1` — potwierdź że wave 1 jest zielone przed twoimi zmianami. Zapisz baseline liczby testów.
4. Uruchom `mcp__claude_ai_humanMCP__get_persona(slug="hodor")` — Hodor to first responder dla wszystkiego z sekretami. Aktywuj go przed pierwszym touch'em na `flyctl secrets`.
5. Uruchom `mcp__claude_ai_humanMCP__get_skill(slug="operational-safety-public")` — reguły bezpieczeństwa. Zwłaszcza sekcja o `flyctl secrets` i `.env`.

Po tych pięciu — zaproponuj kapoostowi Plan (używając ExitPlanMode dopiero po jego akceptacji).

## Zasady non-negocjowalne (te które ludzie łamią najczęściej)

- **Storyboard RED przed kodem.** Napisz storyboard, uruchom, zobacz FAIL, dopiero potem implementuj. To jest SDD w tym repo, nie preferencja.
- **Sabotage-verify po każdym zielonym storyboardzie.** Wywal auth check → storyboard MUSI failować. Wpis do `storyboards/mysloodsiewnia/SABOTAGE_LOG.md`. Bez tego storyboard jest vacuous (patrz `TestNoPhantomToolsInAgentDocs` klasa błędu).
- **Zero sekretów w git.** Nawet w test fixture'ach. Używaj `slug_a`/`slug_b`, `test_token_do_not_use_in_prod`. Jeden `git add tokens.json` na public repo = permanentny leak.
- **Nie mieszaj wave 3 z wave 2 (owner write) ani z horcrux.** Zakres jest wąski: sharing read-only. Jeśli podczas impl wyjdzie że trzeba "przy okazji" dodać write — STOP, spytaj kapoosta.
- **Repo jest publiczne od 2026-08-05.** Każdy commit + każda linia w PR description są indeksowane. Realne slug names znajomych + realne scope patterns identyfikujące dostęp = zakaz. Fixture'y anonimowe.
- **Commit tag `[narada:nar-67cdd80179c2]`** w subject lub body każdego commita wave 3. `/dobranoc` używa tego tagu do rollback tracingu.

## Deliverables sesji (checklista końcowa)

- [ ] Runner rozszerzony o friend tokens (`cfg.FriendTokens`, load w `internal/storyboard/http.go`).
- [ ] Cztery storyboardy z `prompts/wave3-storyboards/` przeniesione do `storyboards/mysloodsiewnia/` i zielone.
- [ ] Każdy storyboard sabotage-verified, wpis w `SABOTAGE_LOG.md`.
- [ ] Kod Fly-side w `internal/mcp/v2/tools/mysloodsiewnia_*.go` + auth w `internal/auth/`.
- [ ] Kod vault-side w `~/mysloodsiewnia/services/humanmcp_bridge.py` + SIGHUP handler + audit writer.
- [ ] Bootstrap body update — wzmiankuje friend token model.
- [ ] `docs/adr/0001-mysloodsiewnia-bridge.md` addendum "2026-XX-XX — wave 3 wired up, deployed".
- [ ] `~/Documents/humanmcp-incident-playbook.txt` rozszerzony o sekcję: dodać friend token, revokować, sprawdzić audit.
- [ ] `go test -race ./... -count=1` zielone + `TestDocsToolCountMatchesReality` OK.
- [ ] `git log --all -S "friend"` na obu repo nie zawiera realnych imion ani realnych scope patternów.
- [ ] Deploy `flyctl deploy --app kapoost-humanmcp`.
- [ ] Manual verify na live: negative paths (out-of-scope 403, unrecognized token unauthorized) — pozytywne testy TYLKO na storyboardach, nie na prod (memory `feedback_prod_verify_hygiene`).

## Blockery / stop conditions

Zatrzymaj się i spytaj kapoosta jeśli:

- Podczas impl okaże się że narada założyła coś nierealistycznie — konsensus 5/5 nie oznacza że nie ma dziury (contrarian znalazł sześć).
- Musisz dodać cokolwiek do `.env`, `flyctl secrets`, albo poza `slug_a`/`slug_b` w fixture'ach.
- Znajdziesz istniejący kod który zakłada `EditToken == owner == unlimited scope` i jest trudny do rozszerzenia — wave 3 nie łamie wave 1 auth, kompatybilność jest twarda.
- Storyboard nie chce failować przy sabotage (znaczy że jest vacuous — napraw storyboard, nie omijaj sabotage).
- Chcesz stworzyć token z `expires_at > 1 rok` albo bez `expires_at` — to horcrux use case, osobny ADR, osobna narada.

Powodzenia. Design jest zamknięty, kod jest twoim jedynym problemem.
