# Smart Legislation Editor — Plan

A drafting environment for New York City Council legislation, built into
intro.nyc at `/editor`.

The editor exists because bill text is not ordinary prose. A bill amending
consolidated law must reproduce the existing text verbatim, bracket everything
being removed, underline everything being inserted, recite the legislative
history of each provision it touches, and refer to other provisions using a
rigid citation grammar. Word processors do none of that; they only make it
possible. This editor makes the required form the default.

Authority for every rule referenced below is the *NYC Bill Drafting Manual,
Third Edition (2022)* (`NYC-Bill-Drafting-Manual-2022-FINAL.pdf`, extracted to
`.txt` alongside it). Rule numbers cited in code comments refer to that manual.

## 1. Scope

**In scope now (phase 1–4):** the editing surface. A drafter can start a bill,
pull a section of existing law into it from a small hard-coded corpus, amend it
with tracked bracket/underline semantics, insert well-formed cross-references,
see style violations flagged live, and export bill text.

**Deliberately deferred:** a real legislation API. Section 8 sketches it. Until
then the corpus is a five-section JSON fixture generated from the American Legal
Publishing XML export, so the editor's data contract is fixed early and the API
can be swapped in behind it without touching editor code.

## 2. Why ProseMirror

The document is a schema, not a blob of HTML. ProseMirror lets us declare that a
bill *is* a title, an enacting clause, and an ordered list of bill sections, each
of which is either unconsolidated text or a lead-in followed by law blocks at
known hierarchical levels. Invalid structures cannot be typed into existence.

Equally important: ProseMirror transactions are interceptable. Tracked-change
semantics — where pressing Backspace over existing law does not remove text but
brackets it — are impossible in a contenteditable div and natural in a
ProseMirror plugin.

No bundler is introduced. ProseMirror ships as ESM and is loaded from a CDN
through an import map, matching how this codebase already loads Bootstrap and
Mapbox.

## 3. Document model

### Nodes

| Node | Content | Notes |
| --- | --- | --- |
| `doc` | `bill_title enacting_clause bill_section+` | |
| `bill_title` | `inline*` | Rule 2.1. Rendered under a centered "A LOCAL LAW". Single subject; describes any repeal. |
| `enacting_clause` | atom | Rule 2.2. Fixed text, underlined, not editable, not line-numbered. |
| `bill_section` | `section_lead law_block*` | Attrs: `num`, `kind` (`amend`/`add`/`repeal`/`unconsolidated`/`effective`), `cite`, `history`. |
| `section_lead` | `inline*` | The unconsolidated lead-in. Never underlined (Rule 3). Auto-composed, hand-editable. |
| `law_block` | `inline*` | Consolidated text. Attrs: `level` (`section`…`item`), `designator`, `heading`, `added` (whole block is new). |

`bill_section` numbering is derived, never stored by hand: the first is
`Section 1.` spelled out, the rest are `§ 2.`, `§ 3.` (Rule 3). The last bill
section is the effective date (Rule 6).

### Marks

| Mark | Meaning | Rendering |
| --- | --- | --- |
| `ins` | Text added by this bill | Underline (Rule 11.1) |
| `del` | Existing text this bill removes | Wrapped in `[ ]` via CSS (Rule 11.1) |

Deleted text is retained in the document and bracketed, never dropped. Where a
deletion and an addition are adjacent, the deletion is kept to the left of the
addition (Rule 11.1).

## 4. Tracked amendment engine

This is the core of the editor. In a `bill_section` of kind `amend`, inside
`law_block` content, the plugin rewrites editing intent:

- **Typing** marks the replaced range `del`, then inserts the typed text after
  it carrying `ins`. Deletion precedes addition (Rule 11.1).
- **Backspace / Delete** applies `del` rather than removing text. Applied to
  text already `del`, it *restores* the text — deletion is reversible without
  undo history.
- **Text the bill itself added** (`ins`) is removed outright; it was never in
  the law, so there is nothing to bracket.
- **Spacing** follows Rule 11.1.2: a space is kept between a bracketed deletion
  and an adjacent underlined addition, and spaces bounding an addition are not
  underlined.
- **Partial words** follow Rule 11.1.1: the selection is expanded to word
  boundaries, so the editor produces `[workman] worker`, never `work[man]er`.
- **Punctuation** follows Rule 11.1.3: a deletion abutting preceding
  punctuation does not gain a space.

In a `bill_section` of kind `add`, all consolidated text is `ins` by
construction and typing behaves normally.

## 5. Section picker

The drafter chooses a provision of existing law and the editor builds a
conforming bill section around it:

1. Pick a section from the corpus, then optionally narrow to a subdivision or
   paragraph. Amending non-consecutive provisions offers the Rule 3.4 choice
   between one bill section with intervening context and separate bill sections.
2. Pick an operation: amend, add a new provision, or repeal.
3. The lead-in is composed from the operation plus the section's amendment
   history, following Rule 3.1:
   - never amended since the 1963 Charter / 1985 Code → no recital (3.1.2)
   - added, never amended → "as added by local law number N for the year Y" (3.1.3)
   - amended → cite only the last amendment (3.1.4)
   - added/amended then redesignated → cite both (3.1.5, 3.1.6)
   - redesignated then amended → cite only the amendment (3.1.7)
   - pure addition or repeal → no recital (3.1.8, 3.1.10)
   - state-enacted → "chapter N of the laws of Y" (3.1.1)
4. The law text is inserted as `law_block`s at their real hierarchy levels,
   ready to be amended under the tracked engine.

A repeal produces `REPEALED` in capitals with no text and no history
(Rules 3.1.10, 11.1.4).

## 6. Reference builder

Cross-references are generated, not typed, so they cannot drift from the grammar
in Rule 5:

- Same body of law → bare section number (5.1.1).
- Different body → "of the charter" / "of the administrative code" (5.1.2).
- Below section level → the full chain up to the section, or "of this
  subdivision"/"of this section" when a common ancestor exists (5.1.3).
- From unconsolidated text → the full name of the body of law (5.1.4).
- City rules, state statutes, NYCRR, U.S. Code, CFR → the per-source forms in
  Rules 5.2–5.6, including the "or a successor provision" tail required for
  rules and regulations.

## 7. Style checks

A live panel reports drafting-manual violations with click-to-locate. Each check
names its rule. The initial set:

- "shall take effect" → "takes effect"; "after its enactment" → "after it
  becomes law" (6.1)
- `shall` outside a duty, and `shall not` vs `may not` (11.16)
- Capitalization of "New York city charter", "administrative code of the city of
  New York", agency names (11.2)
- Double space after a period (11.3); space after `§` (11.4); period after a
  section number (11.5)
- Numerals by default, spelled-out ordinals, spelled-out bill-section references
  (8.1–8.4)
- Section-number citation form `section 17-507(c)` → subunit chain (5.1.3)
- Prohibited and outdated terminology (11.12)
- Contractions (11.22); "which" vs "that" (11.21.1)
- Structural checks: a subdivision `a` with no subdivision `b` (4.3.2); a bill
  that repeals without saying so in its title (2.1.1); a missing effective date
  (Rule 6)

Checks are pure functions over the document, so they are cheap to extend and can
later be reused server-side.

## 8. Export

- **Bill text** — plain text with `[deleted]` and underlining conventions,
  bill-section numbering, and the title/enacting-clause/body/effective-date
  order of Rule 2.
- **Rich text** — HTML shaped for pasting into the Legislative Division's Word
  template: Times New Roman 12pt, double-spaced body, justified.
- **JSON** — the ProseMirror document, for round-tripping and future storage.

Drafts persist to `localStorage`; nothing is sent to the server in phase 1–4.

## 9. Deferred: the legislation API

The editor consumes one shape, defined by `static/editor/law_fixture.json` and
produced today by `scripts/extract_law_fixture.py` from the ALP XML export:

```
{ "sections": [ { "id", "cite", "code", "heading",
                  "history": { "added", "amended", "redesignated", "repealed" },
                  "blocks": [ { "level", "designator", "text" } ] } ] }
```

When the corpus grows past a fixture, the same shape is served from
`GET /editor/api/law?q=` and `GET /editor/api/law/{cite}`, backed by the full
XML export in `gs://intronyc/` and fetched through the existing `App.getFile`
cache. The editor's fetch layer already goes through those URLs, so the swap is
a server-side change only.

## 10. Phases

1. **Skeleton** — route, template, schema, ProseMirror mounting, bill
   scaffold, `ins`/`del` marks and their rendering. *(this change)*
2. **Tracked amendments** — the Rule 11.1 editing engine. *(this change)*
3. **Corpus + picker + references** — fixture loading, lead-in composition,
   reference builder. *(this change)*
4. **Style checks + export.** *(this change)*
5. Real legislation API over the full Administrative Code and Charter.
6. Server-side drafts, sharing, and diffing against a bill's prior version.
7. Resolutions (Rule 10) and Construction Code conventions (Appendix A).
