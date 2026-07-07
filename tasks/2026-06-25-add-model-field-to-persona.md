# Task: dodać pole `model` do Persona struct + parser

**Autor**: Claude w sesji mysloodsiewnia (2026-06-25)
**Cel**: po stronie humanmcp-server zna i wystawia pole `model` z persona MD frontmatter, żeby Fly app `kapoost-humanmcp` widział aktualne modele (np. Opus 4.7 dla Hermiony / dobranoc-recap, Sonnet 4.6 dla 22 innych person).

## Kontekst

W mysloodsiewni (`~/mysloodsiewnia/personas/*.md`) każdy persona MD ma frontmatter z polem `model:`, np.:

```yaml
---
id: hermiona
name: Hermiona
role: Intent Analyst, Context Keeper & Documentation Owner
model: claude-sonnet-4-6
provider: anthropic
active: true
base_prompt: ...
---
```

Sync `POST /sync-humanmcp` (z mysloodsiewnia → `~/humanmcp-server/content/personas/<engine_id>.md`) ZAPISUJE pole `model` jako część frontmatter, ale **obecny parser w humanmcp-server go ignoruje** — `parsePersonaFile` w `internal/mcp/handler.go:170` rozpoznaje tylko 4 pola: `slug`, `title`, `role`, `tags`. `Persona` struct (linia 58) też nie ma `Model` field.

Efekt: Fly app nie wie który model którego persony użyć w MCP tool calls / `/mc` dashboard. Zawsze fallback do hardcoded default w humanmcp Go (sprawdź — pewnie haiku albo sonnet-4 stary).

## Co zrobić

### 1. `internal/mcp/handler.go` — dodać `Model` do Persona struct

```go
type Persona struct {
    Slug  string   `json:"slug"`
    Title string   `json:"title"`
    Role  string   `json:"role"`
    Model string   `json:"model,omitempty"`   // ← nowe
    Tags  []string `json:"tags"`
    Body  string   `json:"body"`
}
```

### 2. `parsePersonaFile` — parsować linię `model:`

W loop po frontmatter (po `} else if strings.HasPrefix(line, "tags:") {`), dodać:

```go
} else if strings.HasPrefix(line, "model:") {
    p.Model = strings.TrimSpace(strings.TrimPrefix(line, "model:"))
}
```

### 3. Storyboard PRZED zmianą (SDD discipline)

Wymóg z `storyboards/README.md` i skill `storyboard-driven-development`: napisz scenkę pinującą zachowanie ZANIM zmienisz kod. Sugerowana scenka `storyboards/mcp/persona_model_field_parsed.yaml`:

```yaml
id: mcp/persona_model_field_parsed
version: "1.0.0"
title: "parsePersonaFile zachowuje model z frontmatter"
kind: unit
narrative: |
  mysloodsiewnia sync push'uje MD z polem `model: claude-sonnet-4-6` lub
  `model: claude-opus-4-7`. parsePersonaFile musi to zachować w
  Persona.Model — bez tego Fly app nie wie który model wywołać.
# ... reszta — dopisz w stylu istniejących scen
```

### 4. Sabotage-test (SDD reguła #2)

- Wykomentuj nowy `} else if strings.HasPrefix(line, "model:") {` blok
- `go test -race ./internal/mcp/...` powinien failować
- Cofnij komentarz, powinien przejść

### 5. Deploy

Po zielonych testach:
```bash
cd ~/humanmcp-server
deploy-mc                  # alias z ~/.zshrc, lub klawisz D w mysloodsiewnia launcher
```

To wgra fresh ffmpeg-server na Fly + smoke test "template error" w fly logs (lekcja 2026-05-20).

## Verification (po deploy)

```bash
# 1. Persona endpoint MCP zwraca pole model:
curl -s https://kapoost-humanmcp.fly.dev/api/personas \
  | jq '.[] | select(.slug == "hermiona") | {slug, model}'

# Oczekiwane:
# { "slug": "hermiona", "model": "claude-sonnet-4-6" }
# (NIE pusty string ani brak pola)

# 2. /mc dashboard pokazuje modele per persona
open https://kapoost-humanmcp.fly.dev/mc
# Hermiona, Mira, etc. powinny mieć kolumnę z modelem
```

## Storyboardy w humanmcp-server które już dotykają person

- `storyboards/mcp/collection_access_gates_via_mcp.yaml` — auth flow
- `storyboards/mcp/normalizepoem_tolerates_polish_diacritics.yaml` — content normalize

Żaden nie pinuje pól per persona. Nowy `persona_model_field_parsed` byłby pierwszy w tej klasie.

## Niezwiązane (uważaj)

- NIE ruszaj `loadPersonasList` w `internal/web/handler.go:2610` ani `loadPersonas` w `internal/mcp/handler.go:147` — one tylko czytają dir + delegują do `parsePersonaFile`. Wystarczy zmiana w parserze.
- NIE rób edycji MD plików ręcznie — `~/humanmcp-server/content/personas/*.md` jest **target sync** z mysloodsiewnia (od dziś, fix v3). Każda edycja zostanie nadpisana przy kolejnym `POST /sync-humanmcp`. Edytuj zawsze w `~/mysloodsiewnia/personas/<id>.md` (source of truth).

## Po zakończeniu

Powiedz mi (sesja mysloodsiewnia) gdy zrobione — sprawdzę z mojej strony czy `curl /api/personas` zwraca model i mogę zapisać memory że pełen flow działa.
