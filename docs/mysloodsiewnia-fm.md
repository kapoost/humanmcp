---
title: "MYŚLOODSIEWNIA — Field Manual"
subtitle: "Instrukcja dla przyjaciół"
author: "kapoost"
date: "2026-04-19"
geometry: margin=2cm
fontsize: 11pt
mainfont: "Helvetica"
monofont: "Menlo"
header-includes:
  - \usepackage{fancyhdr}
  - \pagestyle{fancy}
  - \fancyhead[L]{\small MYŚLOODSIEWNIA FM}
  - \fancyhead[R]{\small POUFNE — TYLKO DLA PRZYJACIÓŁ}
  - \fancyfoot[C]{\thepage}
  - \usepackage{awesomebox}
  - \usepackage{tcolorbox}
---

\begin{center}
\Large\textbf{MYŚLOODSIEWNIA}\\[0.3em]
\normalsize Field Manual v1.0 — Instrukcja dla przyjaciół\\[0.5em]
\small Wersja: 2026-04-19 · Autor: kapoost\\[1em]
\rule{0.6\textwidth}{0.4pt}
\end{center}

\vspace{1em}

# Co to jest

Myśloodsiewnia to **osobisty skarbiec wiedzy** — w pełni lokalny, offline, bez chmury. Zbiera, transkrybuje, indeksuje i przeszukuje Twoje notatki, dokumenty PDF, kontakty i nagrania głosowe.

Wszystko zostaje na Twoim komputerze. Zero telemetrii. Zero chmury.

**Stack:** Python 3.10+, FastAPI, SQLite + FTS5, Whisper (large-v3), pyannote (diaryzacja).

**Port:** `7331` (domyślnie)

---

# Uruchamianie

## Metoda 1: Alias w terminalu

```bash
mysloodsiewnia
```

Alias powinien być skonfigurowany w `~/.zshrc`. Jeśli go nie masz — użyj metody 2.

## Metoda 2: Launcher TUI

```bash
cd ~/mysloodsiewnia
python launcher.py
```

Otworzy się interfejs w stylu Norton Commander:

| Klawisz | Akcja |
|---------|-------|
| **F2** | Start serwera |
| **F3** | Stop serwera |
| **F4** | Konfiguracja |
| **F5** | Pokaż log |
| **F8** | Otwórz Web UI |
| **F10** | Wyjście |

## Metoda 3: Bezpośrednio

```bash
cd ~/mysloodsiewnia
uvicorn main:app --host 0.0.0.0 --port 7331
```

---

# Konfiguracja

Zmienne środowiskowe (w pliku `.env` lub eksportowane w shellu):

| Zmienna | Domyślna | Opis |
|---------|----------|------|
| `WHISPER_MODEL` | `large-v3` | Model transkrypcji |
| `VAULT_PORT` | `7331` | Port serwera |
| `USE_SSL` | `false` | HTTPS (opcjonalne) |
| `HF_TOKEN` | *(brak)* | Token Hugging Face — **wymagany** do diaryzacji |

**Uwaga o HF_TOKEN:** Bez niego transkrypcja działa, ale nie rozróżnia mówców. Zarejestruj się na huggingface.co i wygeneruj token. Zaakceptuj warunki modelu pyannote/speaker-diarization.

---

# Codzienne użycie

## Przeszukiwanie

**Web UI:** Otwórz przeglądarkę na `http://localhost:7331/search`

**API (curl):**

```bash
curl -s http://localhost:7331/query \
  -H "Content-Type: application/json" \
  -d '{"q": "twoje zapytanie"}' | python -m json.tool
```

**CLI:**

```bash
python cli.py search "twoje zapytanie"
```

Wyszukiwarka używa BM25 ranking (FTS5) — rozumie polskie frazy, sortuje po trafności.

## Dodawanie dokumentów

### PDF
```bash
python cli.py add-pdf ~/Documents/dokument.pdf
```
Lub przez API:
```bash
curl -X POST http://localhost:7331/ingest/pdf \
  -F "file=@dokument.pdf"
```

### Notatka tekstowa
```bash
curl -X POST http://localhost:7331/ingest/note \
  -H "Content-Type: application/json" \
  -d '{"title": "Tytuł", "content": "Treść notatki"}'
```

### Kontakt
```bash
curl -X POST http://localhost:7331/ingest/contact \
  -H "Content-Type: application/json" \
  -d '{"name": "Jan Kowalski", "email": "jan@example.com", "notes": "Kolega z regat"}'
```

## Transkrypcja nagrań

### Wrzuć plik audio
```bash
curl -X POST http://localhost:7331/transcripts/upload \
  -F "file=@nagranie.mp3"
```

### Transkrypcja na żywo (WebSocket)
Otwórz `http://localhost:7331/ws/transcribe` — mikrofon → tekst w czasie rzeczywistym.

### Podsumowanie transkrypcji
```bash
curl http://localhost:7331/transcripts/{id}/summarize
```

### Eksport
```bash
curl http://localhost:7331/transcripts/{id}/export?format=md
```

## Leksykon

Myśloodsiewnia automatycznie wyciąga kluczowe terminy z transkrypcji i buduje osobisty leksykon.

```bash
# Przejrzyj leksykon
curl http://localhost:7331/lexicon

# Dodaj ręcznie termin
curl -X POST http://localhost:7331/lexicon \
  -H "Content-Type: application/json" \
  -d '{"term": "baksztag", "definition": "Kurs żaglowy — wiatr od rufy ukośnie"}'
```

---

# Narada — Zespołowa analiza tematu

Myśloodsiewnia ma wbudowany moduł **narad** — możesz zapytać o zdanie kilku ekspertów AI (personas) jednocześnie.

## Jak to działa

1. Wybierasz temat
2. Dobierasz skład ekspertów (lub używasz gotowego)
3. Każdy ekspert odpowiada ze swojej perspektywy
4. Hermiona zbiera ustalenia

## Gotowe składy

| Temat | Skład |
|-------|-------|
| **Security review** | Ghost (Red Team) + Yuki (Blue Team) + Harvey (Prawo) |
| **Architektura** | Mira Chen (Inżynieria) + Axel (QA) + Hermes (Proces) |
| **Design & komunikacja** | Eleanor (UX) + Sophia (Persuazja) + Łukasz Mazur (Kontrarian) |
| **Dane & AI** | Tomas (Data/ML) + Mira + Zara (Prompty) |
| **Deep research** | Julka (Nauka) + Ela (Biznes) — para dialektyczna |

## Meta-warstwa (zawsze aktywna)

- **Zara** — projektuje interakcje między ekspertami
- **Hermiona** — dokumentuje ustalenia i decyzje
- **Hermes** — pilnuje postępu, wykrywa kręcenie się w kółko
- **George Carlin** — mówi na koniec, niekomfortowe prawdy

## Użycie przez API

```bash
curl -X POST http://localhost:7331/personas/discuss \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Czy powinniśmy zmienić architekturę auth?",
    "personas": ["ghost", "yuki", "harvey", "mira-chen"]
  }'
```

---

# Komunikacja — zasady Sophii

Sophia Marchetti (persona ds. komunikacji) podpowiada jak formułować zapytania i prezentować wyniki:

## Zanim zapytasz — odpowiedz sobie

1. **Kto jest odbiorcą?** (technik, biznes, prawnik)
2. **Jaki jest frame?** (problem, szansa, ryzyko)
3. **Gdzie jest opór?** (co odbiorca może odrzucić)
4. **Co jest niewypowiedziane?** (ukryte założenia)

## Format odpowiedzi

- **Zacznij od konkluzji** — nie od kontekstu
- **Jedno zdanie na decyzję** — nie trzy akapity
- **Fakty → interpretacja → rekomendacja** — w tej kolejności
- **Jeśli nie wiesz — powiedz „nie wiem"** — zero halucynacji

---

# Rozwiązywanie problemów

## Serwer nie startuje

```bash
# Sprawdź czy port jest wolny
lsof -i :7331

# Zabij proces jeśli wisi
kill -9 $(lsof -ti :7331)

# Uruchom ponownie
python launcher.py  # F2
```

## Transkrypcja nie działa

1. Sprawdź czy model Whisper jest pobrany:
   ```bash
   python -c "import whisper; whisper.load_model('large-v3')"
   ```
2. Sprawdź `HF_TOKEN` jeśli diaryzacja nie działa
3. Sprawdź logi: `F5` w launcherze lub `tail -f logs/vault.log`

## Brak wyników wyszukiwania

- Upewnij się, że dokumenty zostały zaindeksowane (`/ingest`)
- Spróbuj prostszego zapytania (jedno–dwa słowa)
- Sprawdź czy baza istnieje: `ls vault.db`

## Baza jest za duża / wolna

```bash
# Sprawdź rozmiar
du -sh vault.db

# Optymalizuj FTS
sqlite3 vault.db "INSERT INTO documents_fts(documents_fts) VALUES('optimize');"
```

---

# Bezpieczeństwo

- Myśloodsiewnia działa **tylko lokalnie** — nie wystawiaj portu 7331 na świat
- Jeśli musisz udostępnić zdalnie — użyj **Tailscale** (mesh VPN, zero konfiguracji)
- Baza `vault.db` zawiera Twoje dane — rób backupy
- `HF_TOKEN` jest poufny — nie commituj go do repo

---

# Szybka ściągawka

```
mysloodsiewnia          # start (alias)
http://localhost:7331   # web UI
F2 start · F3 stop · F5 log · F8 browser · F10 quit

# szukaj
curl localhost:7331/query -d '{"q":"szukana fraza"}'

# dodaj PDF
curl -X POST localhost:7331/ingest/pdf -F "file=@plik.pdf"

# transkrybuj
curl -X POST localhost:7331/transcripts/upload -F "file=@audio.mp3"

# narada
curl -X POST localhost:7331/personas/discuss \
  -d '{"topic":"temat","personas":["ghost","mira-chen"]}'
```

---

\begin{center}
\small\textit{Myśloodsiewnia — bo wiedza, której nie przeszukasz, nie istnieje.}\\[0.5em]
\small — kapoost, 2026
\end{center}
