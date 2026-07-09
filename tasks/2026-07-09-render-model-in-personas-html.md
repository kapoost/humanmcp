# Task: pokaż pole `model` w `/personas` HTML page

**Autor**: Claude w sesji mysloodsiewnia (2026-07-09)
**Kontekst**: `d658c0c` dodał `Persona.Model` do MCP handler + parser. Ale web page `/personas` (rendered z `templates/personas.html`) nadal nie wyświetla modelu — bo używa **osobnego** parsera (`loadPersonasList` w `internal/web/handler.go`) który nie ma pola `model` w map, i template nie ma `{{.Model}}`.

## Test failujący (przed zmianą)

```bash
curl -s https://kapoost-humanmcp.fly.dev/personas | grep -oE 'claude-(opus|sonnet|haiku)-[0-9.-]+' | sort -u
# → pusto (2026-07-09, sprawdzone)
```

Po fix + deploy powinno zwrócić:
```
claude-haiku-4-5-20251001
claude-opus-4-7
claude-sonnet-4-6
```

## Trzy zmiany (SDD discipline — storyboard PRZED kod)

### 1. Storyboard najpierw (jak w skill storyboard-driven-development)

Utwórz `storyboards/web/personas_html_renders_model.yaml`:

```yaml
id: web/personas_html_renders_model
version: "1.0.0"
kind: http
title: "GET /personas HTML zawiera modele Anthropic dla person które je mają"
narrative: |
  Persona MD z frontmatter `model: claude-sonnet-4-6` musi trafiać do
  rendered HTML strony /personas. Bez tego dashboard kłamie —
  wyświetla persony bez info który model używają, dopóki ktoś nie
  zajrzy w MCP tool.

  Klasa bugów: dublowanie parsera (loadPersonasList vs parsePersonaFile)
  gdzie jeden dostaje field, drugi zapomina. Scenka pinuje że template
  faktycznie POKAZUJE model, nie tylko że parser go czyta.
setup:
  method: GET
  path: /personas
assertions:
  - kind: body_contains
    substring: "claude-sonnet-4"
  - kind: body_contains
    substring: "claude-opus"
known_regressions_caught:
  - "2026-07-09: parser MCP miał Model od d658c0c ale web loadPersonasList i template go ignorowały — curl+grep zwracał pusto"
sabotage_verified: "2026-07-09"
```

Format YAML dopasuj do istniejącej konwencji w `storyboards/mcp/*.yaml` — jeśli inny styl (setup/steps/assertions), użyj tego. Klucz: scena FAIL'uje przed twoją zmianą, PASS po.

### 2. Parser update w `internal/web/handler.go`

W `loadPersonasList()` (obecnie parsuje `title`, `role`, `prompt`, `tags` z frontmatter linie po linii) dodaj analogicznie:

```go
} else if strings.HasPrefix(line, "model:") {
    model = strings.TrimSpace(strings.TrimPrefix(line, "model:"))
}
```

I w map wynikowym:

```go
out = append(out, map[string]interface{}{
    "Slug":  slug,
    "Title": title,
    "Role":  role,
    "Model": model,      // ← nowe
    "Tags":  tags,
    // ... reszta jak jest
})
```

### 3. Template `internal/web/templates/personas.html`

Linia ~31 ma `<div class="pe-role">{{.Role}}</div>`. Dodaj obok albo pod:

```html
{{if .Model}}<div class="pe-model" title="LLM model">{{.Model}}</div>{{end}}
```

Style CSS `.pe-model` — dopasuj do wizualnej konwencji strony (mała czcionka, muted color, ewentualnie badge).

## Sabotage-check (SDD reguła #2)

Po zielonym storyboard:
1. Wykomentuj linię `"Model": model,` w map
2. Odpal storyboard runner (albo `go test ./internal/web/...` jeśli scena jest tam podpięta)
3. Musi failować
4. Odkomentuj — musi pass'ować

## Deploy

```bash
cd ~/humanmcp-server
# funkcja z ~/.zshrc (jeśli nie ma: fly deploy -a kapoost-humanmcp)
deploy-mc
```

## Verification po deploy

```bash
# 1. Grep HTML na Fly
curl -s https://kapoost-humanmcp.fly.dev/personas | grep -oE 'claude-(opus|sonnet|haiku)-[0-9.-]+' | sort -u

# Oczekiwane:
# claude-haiku-4-5-20251001
# claude-opus-4-7
# claude-sonnet-4-6
```

Jeśli grep zwróci te wartości → end-to-end łańcuch `mysloodsiewnia/personas/*.md` → `POST /sync-humanmcp` → `~/humanmcp-server/content/personas/*.md` → parser web → template → HTML Fly działa.

## Kontekst dla decyzji

Persona `dobranoc-recap` (skill nie persona, ale example modelu) i persona `hermiona` powinny mieć `claude-opus-4-7`. Reszta z 22 person: `claude-sonnet-4-6`. Dwie na `claude-haiku-4-5-20251001` (Axel, jedna inna).

Jeśli po deploy widzisz np. `claude-sonnet-4-20250514` (Sonnet 4.0 stary z 2025-05-14) — to oznacza że sync z mysloodsiewni nie doszedł do humanmcp-server od dawna. Ale `sync-humanmcp` z dzisiaj przetestował `ok 24 md`, więc pliki są aktualne.

## Nie ruszaj

- `parsePersonaFile` w `internal/mcp/handler.go` — zrobione w d658c0c, plus w tej sesji sesji poprzedniej (SDD storyboard dla mcp parser). To INNY parser dla MCP tool `list_personas`, nie dla web page.
- `content/personas/*.md` — to sync target z mysloodsiewni, każda ręczna edycja zostanie nadpisana przy `POST /sync-humanmcp`. Modele edytuj w `~/mysloodsiewnia/personas/<id>.md` (source of truth).

## Po skończeniu

Powiedz sesji mysloodsiewnia (mnie) gdy `curl … | grep` zwróci coś — zapiszę memory że `mysloodsiewnia ↔ humanmcp` sync + render pole model działa end-to-end.
