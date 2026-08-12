# Kickoff: horcrux narada + ADR-0002 (dead-man vault access)

> Wklej ten prompt w świeżą sesję (Plan mode w Claude Code, cwd = `/Users/kapoost/humanmcp-server`). Prompt samodzielny — nie zakłada pamięci wcześniejszych sesji. Odpal dopiero **po** że wave 3 sharing (`prompts/wave3-impl-kickoff.md`) jest live i stabilne 30 dni — horcrux buduje na tym samym mechanizmie, ale operacyjna dyscyplina jest inna. Nie startuj implementacji przed tym promptem — najpierw narada, potem ADR-0002.

## Co ma się stać w tej sesji

Dwie rzeczy, w tej kolejności:

1. **Nowa narada** (`mcp__claude_ai_humanMCP__run_narada`) o horcrux tokenach — długo-terminowy dostęp do vaultu dla zaufanego powiernika na wypadek gdyby coś się kapoostowi stało. Threat model jest **inny niż sharing** (recipient może nie wiedzieć że ma token do triggeru; kapoost nie może być w loopie renewal).
2. **Draft ADR-0002** — `docs/adr/0002-horcrux-vault-access.md` — z chosen design po naradzie. Nowy ADR (nie addendum do 0001), bo scope jest osobny.

**Nie implementuj kodu w tej sesji.** Design → ADR → osobna sesja impl.

## Kanony do przeczytania NA POCZĄTKU

1. **`docs/adr/0001-mysloodsiewnia-bridge.md`** — cała sekcja "Wave 3 — sharing / friend tokens", w szczególności podsekcja **"Horcrux vs sharing token (Z6)"** (dopisana 2026-08-12 po contrarian pass). To jest jedyne miejsce w repo gdzie horcrux use case jest wprost udokumentowany. Zawiera:
   - Dlaczego sharing token != horcrux token (renewal wymaga interakcji kapoosta, dokładnie state który horcrux ma przetrwać).
   - Wyliczenie mechaniki którą trzeba zaprojektować (dead-man switch? auto-renew? absent heartbeat trigger?).
   - Zakaz tworzenia jakiegokolwiek tokena z `expires_at > 1 rok` przed osobnym ADR.

2. **`prompts/wave3-sharing.md`** — brief wave 3 sharing (kanonu formatu briefu + specyfikacji). Horcrux dostanie własny brief w sesji impl, ten pokazuje jak wygląda gotowy dokument.

3. **`storyboards/mysloodsiewnia/`** + `prompts/wave3-storyboards/` — konwencja SDD którą horcrux odziedziczy.

## Kontekst threat model (dla narady)

Horcrux to nie sharing. Trzy różnice fundamentalne:

- **TTL**: sharing token 30-90 dni z rotacją. Horcrux token — długi lub open-ended, renewal bez kapoosta.
- **Trigger**: sharing token używany na bieżąco (znajomy czyta poezję). Horcrux używany **rzadko lub nigdy** za życia kapoosta — może aktywacja przy triggerze (dead-man switch, brak heartbeatu, notary attest, hardware token przez zaufaną osobę).
- **Awareness**: sharing recipient wie że ma token. Horcrux recipient może nie wiedzieć, dopóki trigger się nie odpali — inne wymagania na "friendly" bootstrap (recipient musi umieć odkryć że ma dostęp i jak z niego skorzystać w kryzysie).

Threat model shift względem wave 3 sharing:

- **Sharing**: adversary = laptop frienda skradziony, friend sam się kompromituje. Rozwiązanie: rewokacja + audit + rate limit.
- **Horcrux**: adversary = *kapoost może być nieosiągalny* (śmierć, ciężka choroba, uwięzienie, zaginięcie na morzu). Trigger musi być **odporny na to** że kapoost nie może anulować false-positive triggeru. False positive activation = nieautoryzowany dostęp bez możliwości odwołania.

## Prompt narady (do wklejenia w `run_narada(context=...)`)

Skopiuj to jako `context` w wywołaniu `mcp__claude_ai_humanMCP__run_narada`:

```
Horcrux vault access — długoterminowy dostęp do mysłoodsiewni (vault
kapoosta, ~9k dokumentów, live na dom) dla zaufanego powiernika na
wypadek gdyby kapoost był nieosiągalny (śmierć, choroba, morze,
uwięzienie).

Stan: wave 3 sharing/friend tokens live (ADR-0001 sekcja Wave 3,
narada nar-67cdd80179c2). Mechanizm friend token istnieje: per-token
scope, per-token rate limit, `expires_at` payload, immediate
revocation przez `flyctl secrets unset`. Ale sharing != horcrux —
sharing renewal wymaga kapoosta, a horcrux ma przetrwać dokładnie
ten stan.

Pytania do rozstrzygnięcia:

1. TRIGGER — jak horcrux token się aktywuje? Opcje:
   (a) Dead-man switch: heartbeat od kapoosta co N dni, brak → token
       auto-aktywny.
   (b) Notary attest: powiernik przedstawia dowód (akt zgonu, court
       order, medical statement) → manual review → token aktywacja.
   (c) Multi-party trigger: N z M zaufanych osób potwierdza →
       aktywacja.
   (d) Hardware token: kapoost daje fizyczne urządzenie (YubiKey,
       physical envelope), powiernik używa gdy trzeba.
   
   Trade-off: false positive (a) = automatyczna leak przy dłuższym
   urlopie kapoosta. False positive (b)+(c) = adversary z podrobionym
   dowodem. Overhead (d) = fizyczna logistyka.

2. RENEWAL — czy horcrux token wygasa? Jeśli tak, jak renewal działa
   bez kapoosta? Jeśli nie, co jest planem na kompromitację tokena
   który nigdy nie wygasa?

3. AWARENESS — czy powiernik wie że ma horcrux, czy dowiaduje się
   dopiero przy triggerze? Trade-off: świadomość zwiększa ryzyko
   compromise, brak świadomości utrudnia użycie w kryzysie.

4. SCOPE — czy horcrux ma szerszy scope niż standard sharing?
   Full-vault (wszystko poza `access:private`)? Wszystko włącznie
   z private (bo w kryzysie prywatność mniej istotna)? Osobny
   scope-tier `horcrux` z własną semantyką w vault SQL?

5. AUDIT — sharing token ma per-response audit z doc_id hashami.
   Horcrux po triggerze będzie prawdopodobnie robił jednorazowy
   bulk dump. Czy audit ma tu inny kształt (kto, kiedy, jaki
   trigger, snapshot całego bulk read)?

6. REVOCATION — kapoost nie może rewokować horcrux tokena jeśli
   sam jest nieosiągalny (dokładnie sytuacja horcrux). Czy jest
   inny mechanizm rewokacji (multi-party override, time-bounded
   even after trigger, notary rescission)?

7. LEGACY handoff — czy horcrux jest kompatybilny z estate planning?
   Prawnik / testament wskazuje powiernika. Powiernik musi umieć
   udowodnić że jest wskazany. Czy mechanizm wpina się w tradycyjną
   ścieżkę prawną, czy jest osobnym systemem?

Blockery / kwestie meta:

- Repo humanmcp jest publiczne od 2026-08-05. Design horcrux będzie
  widoczny publicznie — potencjalny attacker będzie znał mechanizm
  triggeru. Threat model musi to zakładać.
- False positive triggeru horcrux = katastrofa (leak prywatnych
  dokumentów kapoosta gdy on jeszcze żyje i tego nie chce). Prefer
  false negative (recipient nie może wejść mimo autentycznego
  triggeru) — wtedy problem operacyjny, nie privacy incident.
- Horcrux to nie techniczny problem — to **problem kombinowany
  technika + prawo + zaufanie**. Narada powinna wskazać gdzie
  technika kończy się a zaczyna procedura poza-kodowa.

Personas: chciałbym słyszeć zwłaszcza ghost (threat model),
hodor (operational safety, revocation edge cases), mira-chen
(deployment atomicity multi-party), maruda (co się złamie na
realnym incident'cie), contrarian (5/5 konsensus wave 3 miał
sześć dziur, chcę żeby ktoś od razu szukał).

Proszę o zdecydowane głosy: który wariant triggeru wybierasz i
dlaczego, jak wygląda renewal, jak reagujecie na false positive.
Nie chcę "to zależy" — chcę konkretną rekomendację i uzasadnienie.
```

## Po naradzie — draft ADR-0002

Struktura mirror ADR-0001:

```
docs/adr/0002-horcrux-vault-access.md
- Status: Draft (Accepted po review kapoosta)
- Date: YYYY-MM-DD (dziś)
- Narada: nar-XXXXX (id z run_narada w tej sesji)
- Commit tag: [narada:nar-XXXXX]

## Context
[dlaczego horcrux jest odrębnym problemem od sharing]

## Decision
[chosen trigger, renewal, awareness, scope, audit, revocation, legacy handoff]

## Decisions in detail (H1-H7 od pytań narady)

### H1 — trigger mechanism
### H2 — renewal
### H3 — recipient awareness
### H4 — scope
### H5 — audit shape
### H6 — revocation edge case
### H7 — legacy handoff

## Explicit deferrals

## Non-goals
[co horcrux NIE jest — np. multi-user vault, sharing]

## Consequences

## Rollback plan
[co jeśli ADR okaże się zły PO deployment horcrux tokena]

## Sabotage-verification protocol
[jak testować że trigger faktycznie działa bez czekania na kapoosta]

## References
- narada: nar-XXXXX
- ADR-0001 sekcja "Horcrux vs sharing token (Z6)"
- Memory: project_horcrux, project_hodor_tomorrow (jeśli wciąż aktualne)
```

## Pierwsze pięć akcji

1. Przeczytaj ADR-0001 sekcja "Horcrux vs sharing token (Z6)" — 1 minuta.
2. Uruchom `mcp__claude_ai_humanMCP__get_persona(slug="hodor")` + `get_persona(slug="ghost")` + `get_persona(slug="contrarian")` (jeśli istnieje jako persona, inaczej odpalisz Agent contrarian po naradzie).
3. Odpal `mcp__claude_ai_humanMCP__run_narada(context=<tekst z sekcji "Prompt narady" powyżej>)`.
4. Poczekaj ~60s, `fetch_narada_result(id=...)` do skutku.
5. Zaproponuj kapoostowi Plan (ExitPlanMode dopiero po jego akceptacji): przeczytać głosy → wskazać konsensus lub konflikty → draft ADR-0002.

## Zasady non-negocjowalne

- **Nie implementuj kodu w tej sesji.** Design → ADR → osobna sesja impl.
- **Nie twórz żadnego prawdziwego horcrux tokena** (żadnego `FRIEND_TOKEN_*` z `expires_at > 1 rok`) przed acceptem ADR-0002 przez kapoosta.
- **Repo publiczne** — design ADR-0002 będzie na public repo. Threat model musi to zakładać. Realnych powierników / dat / prawdziwych fixture'ów w ADR **nie**.
- **Commit tag `[narada:nar-XXXXX]`** — tag z NOWEJ narady, nie z `nar-67cdd80179c2` (to była narada wave 3, osobna decyzja).
- Jeśli narada wróci z rozdzielonymi głosami (nie 5/5), **spisz konflikt w ADR jako "Explicit deferrals"** — nie próbuj syntetyzować konsensusu który nie istnieje.

## Blockery / stop conditions

Zatrzymaj się i spytaj kapoosta jeśli:

- Narada wraca "to zależy" bez konkretnej rekomendacji od żadnej persony — reformułuj context, spytaj konkretniej.
- Chcesz uwzględnić w ADR mechanizm który wymaga zewnętrznego serwisu (Cloudflare, AWS, notariusz online) — cross-boundary dependencies to nowa klasa ryzyka, wymaga osobnej dyskusji.
- Podczas draftu ADR okazuje się że horcrux fundamentalnie kłóci się z jakąś decyzją wave 3 (np. wymaga zmiany scope grammar z W1 albo złamania invariantu Z3) — STOP, to sygnał że ADR-0001 wymaga rewizji, nie że ADR-0002 może to obejść.
- Kapoost prosi o "szybką implementację" — odmów, ta sesja jest tylko design.

Powodzenia. Horcrux to nie feature — to protokół zaufania. Miej to na uwadze przy każdej decyzji.
