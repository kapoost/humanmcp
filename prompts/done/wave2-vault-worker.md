# Prompt: mysłoodsiewnia wave 2 — vault-side write worker

> Wklej w świeżą sesję (Plan mode w Claude Code, cwd = `/Users/kapoost/mysloodsiewnia`). Prompt samodzielny — nie zakłada pamięci wcześniejszych sesji. Fly-side wave 2 zamknięty commit `bdae91b` w `~/humanmcp-server`, obecnie live na v375 (deployment `01KZVKJ32E...`). Vault-side worker to ostatni brakujący element.

## Cel

Dopisać handler dla `kind == "write"` do bridge workera w `services/humanmcp_bridge.py`. Bez tego każdy `mysloodsiewnia_write` z Fly wpada w istniejący catch `return None, f"unknown op kind: {kind}"` w `_run()` — Fly zwraca agentowi `{status:"vault_error","error":"unknown op kind: write"}` (truthful stub state, nie łamie wave 1, ale write nie działa end-to-end).

Wave 2 = owner-only ingest. Scoped tokens odrzucane już na Fly-side (`write_denied` przed enqueue) — vault dostaje tylko owner opy w kolejce. Ale defense-in-depth: worker MUSI odrzucić op z `TokenID != ""` jak w wave 3 read pattern (patrz `_run` line ~275 obecnego kodu, block "scoped tokens are read-only in wave 3").

## Kontekst (co JUŻ jest)

**Fly-side (w `~/humanmcp-server`, commit `bdae91b`):**
- `internal/mysloodsiewnia/queue.go` — nowa `OpKind == "write"`. Wire contract: `args = {doc_type, title, body, source_path?, meta?}`, result envelope na success `{slug, created_at}`.
- `internal/mcp/mysloodsiewnia_tools.go` + `internal/mcp/v2/mysloodsiewnia.go` — `toolMysloodsiewniaWrite` / `registerMysloodsiewniaWrite`. Precedence: (1) auth → Unauthorized (2) owner-gate — `{status:"write_denied","reason":"owner_only"}` dla valid friend tokens (3) parse + required args (4) 100 KiB body cap → `{status:"payload_too_large","limit":102400,"got":N}` (5) liveness gate (6) `Enqueue` (unscoped bo owner-path).
- 3 storyboardy: `write_offline_owner_and_validation.yaml`, `write_friend_forbidden.yaml`, `write_body_too_large.yaml` — sabotage-verified.
- ADR-0001 addendum 2026-08-12 „Wave 2 (write) Fly-side wired up" — pełen design + rationale.
- Bootstrap teach + skill body `content/skills/mysloodsiewnia-bridge.json` wzmiankują wave 2.

**Vault-side (w `~/mysloodsiewnia`, obecnie — kapoost już zaczął):**
- `services/humanmcp_bridge.py` `_load_vault_bindings()` już eksponuje `db.save_document`, `db.save_chunks`, `db.get_conn` (obok wave 1 search/get_document/list_documents). To scaffolding pod wave 2.
- `db.py::save_document(slug, title, doc_type, source_path, meta, access="public")` — accept access param (dodane w Tier D dla contact private handling).
- `db.py::save_chunks(doc_slug, chunks, dedup=True)` — zapisuje chunki + FTS auto-sync przez trigger `chunks_ai`.
- `_run(kind, args, token_id, scopes)` obecnie handle'uje `search`, `get`, `list`, `status` inline (linie ~275-360). Block `if handler is not None: ... if token_id: return None, "scoped tokens are read-only..."` chroni wave-2 registry (services/ops).

## Chosen design (do 1:1 impl)

### D1 — Handler w `_run` inline, nie osobny moduł

Wave 2 write path idzie inline w `_run()` z rest of wave-1 kinds (search/get/list/status). Nie do `services/ops/registry.py` bo:
- registry blokuje scoped tokens explicit — write od Fly ma `token_id == ""` (owner), więc by przeszło, ale defense-in-depth wymaga że każdy write path double-checks.
- Prostota debugowania — jedna funkcja, jeden switch.

### D2 — Auto-tag `via:humanmcp-bridge` server-side, nieodwracalne

Vault DODAJE tag `via:humanmcp-bridge` do meta['tags'] przed zapisem — nie z payloadu Fly'a. Nawet jeśli owner sam wpisze niewłaściwy tag, ten hardcoded się doda. Rationale (Maruda, ADR D5): audit trail forfeits value jak agenci mogą override.

### D3 — Slug generacja server-side, deterministyczna

Fly nie wysyła `slug` (bo owner-only + agent nie musi znać naming convention vaultu). Vault generuje: `{doc_type}-{sanitize(title)[:40]}-{unix_timestamp}`. Ewentualne kolizje → `save_document` `ON CONFLICT(slug) DO UPDATE` (istniejący pattern) — ale to POWINNO być rzadko bo timestamp.

### D4 — 100 KiB cap defense-in-depth

Fly już blokuje > 100 KiB przed enqueue. Vault re-checkuje `len(body.encode('utf-8')) > 100 * 1024` w handlerze — belt+suspenders. Fly cap może mieć bug (regression, wave 4 może rozluźnić i zapomni), vault musi zawsze bronić.

### D5 — Idempotency przez `op_id` reuse

Sieciowe retry z Fly'a re-delivered `op_id` do vault workera. Vault MUSI zapisać `op_id → slug` mapping w tabeli `bridge_write_idempotency` (nowa) — druga próba tego samego `op_id` zwraca istniejący slug bez ponownego insertu. Bez tego duplikaty przy sieciowych retry.

### D6 — Scoped token odrzuca WCZEŚNIEJ (defense-in-depth)

```python
if kind == "write" and token_id:
    return None, "write denied: scoped tokens are read-only (wave 3 W5)"
```

Fly powinien nie wysłać scoped op z kind=="write" (owner-gate blokuje przed enqueue), ale jak jednak wyśle → vault odrzuca z jasnym errorem.

## Twarde wymagania

1. **Nie łam wave 1 read path**: search/get/list/status pozostają unchanged w `_run()`. Test: uruchom po zmianie `python main.py` + curl-owe search — musi działać jak przedtem.
2. **Idempotency tabela**: `CREATE TABLE bridge_write_idempotency (op_id TEXT PRIMARY KEY, slug TEXT NOT NULL, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`. Migration w `db.py::migrate_db()` (idempotent).
3. **Cap re-check**: `if len(body.encode("utf-8")) > 100 * 1024: return None, f"payload_too_large: limit=102400 got={N}"`. Fly zwróci to samo agentowi.
4. **Auto-tag hardcoded**: `meta.setdefault("tags", []).append("via:humanmcp-bridge")` PRZED `save_document`. Nawet jeśli meta['tags'] już ma ten tag, dopisujemy — dedup w prezentacji, nie w capture.
5. **Response envelope na success**: `{"slug": generated_slug, "created_at": iso8601_now}`. Musi być JSON-serializable dict; Fly worker envelopes to w `{"status":"online","op_id":X,"result":{...}}` przez `enqueueAndWait`.
6. **Scoped token odrzuć**: `token_id != ""` na kind=="write" → return `(None, "write denied: scoped tokens are read-only")`. Fly powinien już blokować, ale defense.
7. **Test lokalnie zanim commit**: sekwencja curl-owa (patrz sekcja Weryfikacja) — nie polegać wyłącznie na Fly-side storyboards.

## Landminy

- **Nie mieszać z services/ops registry** (Mira's dekompozycja, wave-2 planowany tam ale wave 3 pokazał że registry blokuje scoped tokens explicit → nie użyj registry dla wave 2 write, ponieważ scoped test dla wave 2 by explode'ował, choć nie powinien).
- **Body encoding**: UTF-8 length ≠ len(body) w Pythonie (`len` zwraca character count, nie bytes). `len(body.encode("utf-8"))` dla cap check.
- **Chunk generation**: użyj `_chunk_text(body, page=1, chunk_start_idx=0)` z ingest.py? Albo `save_chunks` z pre-chunked payload? Dla wave 2 v1 zrób jednym chunkiem (page=1, chunk_idx=0) — dedukowane później jeśli agent chce PDF-style multi-page.
- **FTS index**: `save_chunks` już triggeruje `chunks_ai` trigger który wpisuje do `chunks_fts` virtual table. Nie trzeba manual FTS insert.
- **Fly wysyła `token_id: ""` (empty string, nie null)** — Python `if token_id:` traktuje empty string jako False. OK.
- **Repo publiczne (`~/humanmcp-server` od 2026-08-05)** — jak dokument WYSŁANY przez bridge zawiera sekret, ten sekret trafia do vault DB i chunks_fts. Nie łamiące, ale warto: rate limit + owner-only są jedynym bezpiecznikiem.

## Kroki (nie improwizuj)

1. Uruchom `git log --oneline --grep "wave 2\|OpWrite\|mysloodsiewnia_write" -10` w `~/humanmcp-server` — zorientuj się w Fly-side commitach.
2. Przeczytaj `~/humanmcp-server/docs/adr/0001-mysloodsiewnia-bridge.md` addendum 2026-08-12 „Wave 2 (write) Fly-side wired up" (linie ~36-88).
3. Przeczytaj `~/humanmcp-server/internal/mysloodsiewnia/queue.go` `OpWrite` const + wire contract w komentarzu.
4. Przeczytaj `~/humanmcp-server/internal/mcp/v2/mysloodsiewnia.go` `registerMysloodsiewniaWrite` — zobacz co dokładnie Fly enqueue'uje (payload shape).
5. Przeczytaj `services/humanmcp_bridge.py` `_run()` — istniejące handlery search/get/list/status jako wzorzec.
6. Przeczytaj `db.py::save_document` (~linia 359) + `db.py::save_chunks` (~linia 378) + `db.py::migrate_db` (~linia 248 — miejsce na nową idempotency table).
7. Sprawdź czy `~/mysloodsiewnia/friend_tokens.json` (chmod 600) zawiera tokeny (patrz auto-memory `project_wave3_wave2_shipped.md` w `~/.claude/projects/-Users-kapoost-humanmcp-server/memory/`) — write path nie tyka friend tokens, ale audit context.

Po tych krokach: zaproponuj kapoostowi Plan (ExitPlanMode po jego akceptacji).

## Deliverables

1. `db.py::migrate_db()` — dodaj `CREATE TABLE bridge_write_idempotency (op_id TEXT PRIMARY KEY, slug TEXT NOT NULL, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)` + `CREATE INDEX idx_bwi_created ON bridge_write_idempotency(created_at)` (dla przyszłego prune po TTL).
2. `services/humanmcp_bridge.py::_run()` — nowy `if kind == "write":` branch przed catch. Precedence:
   - `token_id` non-empty → reject `"write denied: scoped tokens are read-only"`
   - Idempotency check — `SELECT slug FROM bridge_write_idempotency WHERE op_id = ?` — jeśli match, return `{"slug": row["slug"], "created_at": "<lookup created_at>"}`, `None`.
   - Parse args: doc_type, title, body required; source_path, meta optional.
   - Cap check: `len(body.encode("utf-8")) > 100 * 1024` → `payload_too_large`.
   - Slug gen: `f"{doc_type}-{re.sub(r'[^a-z0-9-]+', '-', title.lower())[:40].strip('-')}-{int(time.time())}"`.
   - Auto-tag: `meta.setdefault("tags", []).append("via:humanmcp-bridge")`.
   - `b["save_document"](slug=..., title=..., doc_type=..., source_path=args.get("source_path"), meta=..., access="public")`.
   - `chunks = [{"page": 1, "chunk_idx": 0, "body": body.strip(), "doc_type": doc_type, "metadata": {"via": "humanmcp-bridge"}}]`.
   - `b["save_chunks"](slug, chunks)`.
   - Idempotency insert: `conn.execute("INSERT INTO bridge_write_idempotency (op_id, slug) VALUES (?, ?)", (op_id, slug))` — wymaga op_id dostępny w `_run()`; obecnie NIE jest przekazywany! Trzeba rozszerzyć signature `_run(kind, args, token_id, scopes, op_id="")` + `_execute_and_report` pass op_id.
   - Return `({"slug": slug, "created_at": now_iso}, None)`.
3. Test suite lokalny (nowy plik `test_wave2_write.py` w root — istnieje już `test_vault.py`, `test_e2e.py` jako wzorzec):
   - Owner write happy path → 200 + slug returned + document in db + chunks_fts findable
   - Cap breach → `payload_too_large` error, nie inserted
   - Idempotency: dwa razy ten sam op_id → jeden insert, drugi return cached slug
   - Scoped token reject → `"write denied"` error, nie inserted
   - Auto-tag: retrieve doc, verify tags include `via:humanmcp-bridge`
4. Restart vault (`python main.py`) + smoke test przez Fly (owner token):

   ```bash
   EDIT_TOK=$(security find-generic-password -s humanmcp-edit-token -w) && \
   curl -s -X POST https://kapoost-humanmcp.fly.dev/mcp \
     -H "Authorization: Bearer $EDIT_TOK" \
     -H "Content-Type: application/json" \
     -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"mysloodsiewnia_write","arguments":{"doc_type":"note","title":"wave 2 smoke test","body":"Prawdziwy owner-write end-to-end. Test od Fly przez queue do vault DB."}}}' \
     | jq -r '.result.content[0].text' && unset EDIT_TOK
   ```

   Oczekiwane: `{"status":"online","op_id":"<hex>","result":{"slug":"note-wave-2-smoke-test-<ts>","created_at":"..."}}`.

5. Verify document existst w vault:

   ```bash
   sqlite3 ~/mysloodsiewnia/db/vault.db "SELECT slug, title, doc_type, meta FROM documents WHERE slug LIKE 'note-wave-2%' ORDER BY id DESC LIMIT 1"
   ```

## Weryfikacja przed pierwszym commitem

- [ ] `python -m pytest test_wave2_write.py -v` zielone (4 case'y minimum)
- [ ] Owner smoke test przez Fly zwraca `status:online` + slug
- [ ] Document faktycznie w `vault.db` (`SELECT` weryfikacja)
- [ ] `chunks_fts` znajduje słowo z bodya (`SELECT ... FROM chunks_fts WHERE body MATCH 'smoke'`)
- [ ] `meta.tags` zawiera `via:humanmcp-bridge` mimo że wysłane bez tego tagu
- [ ] Retry tego samego op_id (patrz Fly queue.go — op_id jest w wire, ale klient nie triggeruje retry ręcznie — więc test przez pytest z fake op_id)
- [ ] Scoped token test — write przez friend token jest już blokowany na Fly, ale defense: manual test z fake op_id + token_id → reject
- [ ] `git log --all -S "write" -- services/` nie zdradza żadnej realnej treści testu (tylko boilerplate)
- [ ] Commit tag `[wave2]` w subject / body

## Referencje

- ADR-0001 addendum 2026-08-12 „Wave 2" — `~/humanmcp-server/docs/adr/0001-mysloodsiewnia-bridge.md`
- Fly commit `bdae91b` — Fly-side wave 2 impl (grep `git log --oneline --grep OpWrite`)
- Auto-memory `project_wave3_wave2_shipped.md` — kontekst dnia deployu
- `feedback_batch_edit_read_first.md` — landmine: batch Edits abort na pierwszym Read-miss, poprawne = Read files before batching Edits
- `feedback_never_print_secrets.md` — smoke test edit token przez Keychain lookup, nie print
