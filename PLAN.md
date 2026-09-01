# Smart Legislation Editor — Plan

A drafting environment for New York City Council legislation. It is its own
site, served at `editor.intro.nyc` in production and under `/editor/` in
development, with its own chrome (`templates/editor_base.html`) rather than the
intro.nyc site nav.

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

**In scope now:** the editing surface. A drafter can start a bill, search the
Charter, Administrative Code and RCNY for a provision, pull it into the bill,
amend it with tracked bracket/underline semantics, insert well-formed
cross-references, see style violations flagged live, share a read-only copy, and
export bill text.

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
| `bill_title` | atom | Rule 2.1. Attrs: `code` (which bodies of law the bill amends) and `subject`. Composed into "To amend …, in relation to …"; edited from the title nav, not in the document, so the required prefix cannot be mangled. |
| `enacting_clause` | atom | Rule 2.2. Fixed text, underlined, not editable, not line-numbered. |
| `bill_section` | `section_lead law_block*` | Attrs: `kind` (`amend`/`add`/`repeal`/`unconsolidated`/`effective`), `cite`, `code`. |
| `section_lead` | `inline*` | The unconsolidated lead-in. Never underlined (Rule 3). Auto-composed, hand-editable. |
| `law_block` | `inline*` | Consolidated text. Attrs: `level` (`section`…`item`), `designator`, `label`. |

`bill_section` numbering is derived, never stored: `Section 1.` spelled out for
the first and `§ 2.`, `§ 3.` thereafter are a CSS counter over document order
(Rule 3), so it cannot drift when sections are inserted or reordered. The last
bill section is the effective date (Rule 6).

Designators are likewise drawn from node attributes by CSS rather than typed, so
a drafter cannot renumber the Administrative Code by putting the cursor in the
wrong place.

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

`Enter` is inert inside amended law: splitting a `law_block` would renumber the
law. A `filterTransaction` backstop rejects any transaction that removes
original text through a path the handlers do not cover — cut, drag, unusual
`beforeinput` events — so the invariant holds regardless of how an edit arrives.

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

## 8. Export and sharing

A download menu offers, from one document model:

- **Copy for Word** — HTML shaped for the Legislative Division's template:
  Times New Roman 12pt, double-spaced body, justified, real `<u>` and literal
  brackets.
- **Copy text / Download .txt** — plain text; `[deleted]` brackets and
  `_underscored_` additions, in the title/enacting-clause/body/effective-date
  order of Rule 2.
- **Copy markdown / Download .md** — markdown has no underline, so additions
  use inline HTML and deletions keep their brackets.
- **Download .html** — a standalone printable document.
- **Download as adopted** — the text as it would read once enacted, with
  bracketed material dropped, for proofreading.
- **Download .json** — the document model, for round-tripping.

**Share** saves the draft and returns a read-only link. That page is rendered
server-side in Go from the stored document (`renderBill` in `editor.go`) and
reuses `static/editor/editor.css`, so a shared bill is byte-for-byte the bill
being drafted and needs no JavaScript.

## 8.1 Persistence

`POST /api/draft` creates or updates a draft; `GET /api/draft/{id}` reads one;
`GET /d/{id}` is the read-only view. Storage is a single JSON file
(`--draft-file`, default `drafts.json`) written atomically.

A draft has two identifiers: an unguessable `id`, which is the share link, and a
`secret`, which is the edit token. The secret is returned only to the client
that saved the draft and is never included in a read response, so possession of
a share link grants reading and not writing. `localStorage` also keeps the last
document, so a reload is instant and a failed save is not data loss.

Accounts, per-user document browsing, and Firestore-backed storage are a later
revision; the handler boundary is where that swap happens.

## 9. The law API

Law comes from [nyc_code_archive](https://github.com/jehiah/nyc_code_archive),
which files every provision of the Charter (770 sections), the Administrative
Code (26,731) and the RCNY (8,219) as one JSON document under the path its
citation implies, alongside a per-dataset `index.json` and a `manifest.json`
recording what the text is current through.

| Endpoint | Purpose |
| --- | --- |
| `GET /api/law/datasets` | the bodies of law available, with the publisher's currency statement |
| `GET /api/law/search?q=&dataset=` | find a provision by citation or heading |
| `GET /api/law/section/{dataset}/{file}` | one provision, straight from the archive |

The archive is read from a local checkout in development (`--law-path`, which
defaults to `../nyc_code_archive` when it exists) and from `gs://intronyc/law/`
in production, through the same `App.getFile` cache the rest of the site uses.
Search flattens each dataset's `index.json` once and keeps the result for an
hour; the section endpoint is a straight file read.

The archive's `history` object carries exactly what a Rule 3.1 recital needs:
the law that last amended a provision, the adding law if it was never amended,
and a redesignation that happened after the last amendment.

Two shape details the editor has to respect: `blocks` may contain tables and
other non-text entries, which are not selectable and must be added by hand; and
search is offered over the Charter and Administrative Code by default, because a
local law amends consolidated law and not agency rules (Rule 5.2).

## 10. Phases

1. **Skeleton** — route, template, schema, ProseMirror mounting, bill
   scaffold, `ins`/`del` marks and their rendering. *(done)*
2. **Tracked amendments** — the Rule 11.1 editing engine. *(done)*
3. **Corpus + picker + references** — lead-in composition, reference builder.
   *(done)*
4. **Style checks + export.** *(done)*
5. **Its own site, sharing and persistence** — editor chrome, title nav, draft
   API, read-only view. *(done)*
6. **The full law archive** — search and fetch over the Charter, Administrative
   Code and RCNY. *(done)*
7. Accounts and a draft browser, with documents in Firestore; diffing a bill
   against its prior version.
8. Whole-bill features: declarations of intent, short titles, sunset and
   severability clauses, reporting requirements (Rule 7); definitions helpers
   (Rule 9); the Rule 6 effective-date assistant.
9. Resolutions (Rule 10) and Construction Code conventions (Appendix A).

## 11. Files

| Path | Role |
| --- | --- |
| `editor.go` | page handlers, draft API, server-side bill rendering |
| `editor_law.go` | the law API over nyc_code_archive |
| `editor_draft.go` | the draft store |
| `templates/editor_base.html` | the editor site's chrome |
| `templates/editor.html` | title nav, control bar, dialogs |
| `templates/bill_readonly.html` | the shared read-only view |
| `static/editor/editor.css` | printed-bill styling, shared by both views |
| `static/editor/js/schema.js` | document model |
| `static/editor/js/track.js` | Rule 11.1 amendment engine |
| `static/editor/js/corpus.js` | law search, lead-ins, history recitals |
| `static/editor/js/refs.js` | Rule 5 cross-references |
| `static/editor/js/lint.js` | style checks |
| `static/editor/js/serialize.js` | export |
| `static/editor/js/drafts.js` | persistence |
| `static/editor/js/main.js` | wiring |
