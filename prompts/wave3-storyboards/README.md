# Wave 3 storyboards — executable spec, RED do momentu impl

Cztery pliki `.yaml` w tym katalogu to **executable acceptance tests** dla wave 3 (sharing / friend tokens). Nie są w `storyboards/` bo:

1. Runner (`internal/storyboard/`) picka wszystkie `*.yaml` rekurencyjnie — wrzucone tam **zbroją CI na czerwono** przed impl.
2. Wymagają dwóch rozszerzeń runnera których jeszcze nie ma:
   - Loading friend tokens do `cfg` (`cfg.FriendTokens map[string]FriendTokenSpec` albo podobnie — patrz brief).
   - Wsparcie assertion sequencing z rate limiting (istniejący runner odpala assertions sekwencyjnie — to działa, ale rate limit potrzebuje mock'a zegara albo szybkiego okna).

## Kiedy movać do `storyboards/mysloodsiewnia/`

Po tym jak wave 3 impl session skończy się z:

- [ ] `internal/storyboard/http.go` załadował friend tokens przez `cfg.FriendTokens`.
- [ ] `IsAuthorizedRequestByHeaders(r, scopes)` istnieje w `internal/auth/`.
- [ ] Rate limiter per-token wyeksponowany z overridem zegara (fake clock w testach).
- [ ] Storyboardy tu przechodzą lokalnie po `mv prompts/wave3-storyboards/*.yaml storyboards/mysloodsiewnia/`.

Wtedy: `mv` + sabotage-verify każdego (usuń auth check, storyboard MUSI failować) + wpis do `storyboards/mysloodsiewnia/SABOTAGE_LOG.md`.

## Pokryte failure modes

| Storyboard | Pin |
|---|---|
| `wave3_scoped_out_of_scope_403.yaml` | Z2 asymetria — out-of-scope zwraca 403 z `allowed:[...]` (nie zero rows) |
| `wave3_scoped_offline_same_gate.yaml` | Friend tokens hitują ten sam liveness gate co owner (nie "revoked" ani "forbidden" gdy vault offline) |
| `wave3_unrecognized_token_unauthorized.yaml` | Nieznany friend token (revoked, typo, phishing przechwycony) → Unauthorized identyczne z anonimowym; brak leak informacji |
| `wave3_rate_limit_per_token.yaml` | Prerequisite — 51 request w oknie 50/hr zwraca 429; counter per-token |

## Co NIE jest tutaj (świadome pominięcia)

- **`access:private` invisibility (W3)** — to test vault-side (Python), nie Fly-side. `AND access != 'private'` żyje w SQL layer vaultu, storyboard na Fly go nie dotknie. Test w repo mysłoodsiewnia: `services/tests/test_scoped_query_hides_private.py` (do napisania w wave 3 impl session).
- **Live positive path (scoped read → docs)** — wymaga mock'a bridge (SetBridge + fake liveness). Ta klasa nie istnieje w wave 1 storyboardach (wszystkie testują offline path). Positive path testowany manual verify na prod (memory `feedback_prod_verify_hygiene`), z ostrożnością żeby nie zaśmiecić `stats.ndjson`.
- **Deployment atomicity Fly ↔ vault (Z3 pin)** — cross-process cross-machine synchronization, storyboard się nie nadaje. Manual verify runbook + monitoring alert (do dopisania w `humanmcp-incident-playbook.txt`).

## Konwencje w tych storyboardach

- Token slugi w fixture'ach: `slug_a`, `slug_b` — **nigdy realne imiona** (Hodor Z2 landmine, patrz brief sekcja Landminy).
- Header pattern: `Authorization: Bearer storyboard-friend-<slug>` (edit token nadal używa `storyboard-test-token`).
- `narrative` każdego storyboard cite'uje: narada `nar-67cdd80179c2` + numer contrarian findings (Z1-Z6), żeby regresja była linkowalna do decyzji.
