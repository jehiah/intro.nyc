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

### Repeals (Rule 11.1.4, Appendix F)

A repeal is a different act from an amendment, so the picker asks what is being
repealed rather than inferring it: the whole section, or named subunits.

* Several subunits of one level collapse to a plural with the verb agreeing —
  "Subdivisions a and b of section 17-513 … **are** REPEALED." A single target
  reads "… **is** REPEALED."
* Rule 2.1.1 requires the title to identify and describe what is repealed, so
  the editor offers to append the clause itself: "… and to repeal subdivisions a
  and b of section 17-513 of such code, relating to rules". "Such code" is used
  only when the title already names that body of law by itself.
* No recital of legislative history accompanies a repeal (Rule 3.1.10), and the
  bill section carries no law text — a repeal names the provision and stops.
* The two Appendix F items the editor cannot verify are surfaced as standing
  reminders: cross-references to the repealed provision must be found and dealt
  with, and repealing a repeal does not revive the earlier provision.

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

## 8.1 Accounts, documents and sharing

Authentication and storage follow
[legislation.support](https://github.com/jehiah/legislation.support): Firebase
issues an ID token in the browser, `POST /data/session` exchanges it for a
session cookie, and every request is identified by verifying that cookie. The
cookie is HttpOnly and the client keeps no copy of the token
(`firebase.auth.Auth.Persistence.NONE`). `/__/auth/` is reverse-proxied to the
Firebase-hosted sign-in helpers so the flow stays first-party, which Safari
requires.

The editor home is the sign-in page when signed out and the document list when
signed in. "New draft" asks for the two things a bill cannot be started without
— which bodies of law it amends and its subject (Rule 2.1) — then opens the
editor. Document ids are UUIDs.

Documents live in Firestore under `editor_documents`, with the ProseMirror
document stored as JSON text; Firestore's field-name rules do not survive an
arbitrary document tree.

| Endpoint | Purpose |
| --- | --- |
| `GET /` | sign-in, or the document list |
| `GET|POST /new` | create a bill |
| `GET /d/{uuid}` | the editor, or the read-only bill, by access |
| `GET|POST /api/draft/{uuid}` | load and save |
| `POST /api/share/{uuid}` | update sharing (owner only) |

**Sharing is by email address**, because a drafter shares with colleagues before
knowing whether they have signed in yet. A document carries `Editors` and
`Viewers` lists plus a `Public` flag, and access resolves in one place
(`Document.AccessFor`): owner, then editor, then viewer, then public-view, then
nothing. Edit access supersedes view access, and only the owner may change
sharing. `/d/{uuid}` is a single canonical URL that renders the editor or the
read-only bill depending on what the reader may do.

`localStorage` keeps a copy of the last state per document. It is a fallback for
a save that did not reach the server, and the editor says so plainly rather than
reporting success.

## 8.2 Configuration

`--firebase-project` (default `intro-nyc`) covers Auth and Firestore.
`--firebase-api-key` and `--firebase-app-id` are the public web client config and
also read from `FIREBASE_API_KEY` / `FIREBASE_APP_ID`; without them the sign-in
page says so instead of failing silently.

## 8.3 The read-only view

**Share** opens the sharing dialog; the read-only bill at the same `/d/{uuid}`
is rendered server-side in Go from the stored document (`renderBill` in
`editor.go`) and reuses `static/editor/editor.css`, so a shared bill is
byte-for-byte the bill being drafted and needs no JavaScript.

## 8.4 Billing

Export, sharing with more than one person (including a public link), and a
sixth draft are Plus features. A free account is otherwise fully functional —
it drafts, amends and checks a bill exactly as Plus does.

Plus is a PayPal subscription, monthly ($6.99) or annual ($70, a 20% discount
over monthly). `/billing` shows the current plan and, for a free account, a
PayPal button per interval; a signed-in profile links there from a plan
summary. Sharing sets the subscription's `custom_id` to the drafter's uid, so
both the approval callback and the webhook can attribute a subscription
without a lookup table.

The flow has two halves:

1. **Approval.** `actions.subscription.create` on the PayPal button carries
   the uid as `custom_id`. Its `onApprove` posts the resulting subscription id
   to `POST /api/billing/subscribe`, which fetches that subscription from
   PayPal directly — never trusting the browser — checks its plan id is one of
   ours, its `custom_id` matches the signed-in drafter, and its status is
   active or pending, then stores the record.
2. **Ongoing status.** `POST /webhooks/paypal` verifies each delivery with
   PayPal's own verify-webhook-signature call and updates the stored status on
   `BILLING.SUBSCRIPTION.ACTIVATED` / `UPDATED` / `CANCELLED` / `SUSPENDED` /
   `EXPIRED`, so a lapsed or failed payment revokes Plus access without the
   drafter doing anything.

`/billing` tells a subscriber what they are paying for: the interval and its
price, what Plus includes, when they were last charged and for how much, and
when it renews. Those dates come from PayPal's `billing_info`, mirrored onto
the stored record by `paypalSubscription.applyTo` and refreshed from PayPal
whenever the page is viewed — PayPal is the source of truth, and the mirror
only exists so the page still answers the question when PayPal is unreachable.
`applyTo` copies each field only when PayPal actually sent it, because a
cancellation event carries no `next_billing_time` and blanking the stored one
would lose the date the drafter is paid through.

Cancelling does not take back what was already bought. PayPal charges a full
cycle and does not refund it, so `Subscription.AccessUntil` holds the renewal
that will now never happen and `Active()` keeps Plus on until then; the page
says "Plus ends <date>" rather than offering a plan the drafter still has. A
`SUSPENDED` subscription — a failed payment — is different: access stops now,
and the explanation sits above the upgrade offer, since the drafter is on the
free plan and would otherwise just see a sales pitch for something they
thought they had.

Prices and the feature list live in `plusPlans` and `plusFeatures` in
`editor_billing.go`, and both the upgrade offer and the subscribed status
render from them, so the two panels cannot quote different numbers.

A free-plan limit is enforced once, server-side, on the same handler the web
app, the JSON API and the MCP tools all share: `EditorCreateDraft` /
`EditorNewPost` reject a sixth draft, and `EditorShare` rejects a second
collaborator or a new public link. The editor's JS mirrors these limits (a
disabled-looking export menu, a note in the share dialog) so the drafter is
told before they are turned away, but the server is the actual gate. Export is
the exception: it is generated in the browser from a document the drafter is
already entitled to read, so its gate is a product nudge rather than a
boundary.

Limits never trap a drafter in their own work. A draft that is already shared
more widely than the free plan allows — because a subscription lapsed — can
still be narrowed; only widening it is refused. Drafts over the limit are kept
and editable; only a new one is refused.

| Endpoint | Purpose |
| --- | --- |
| `GET /billing` | plan summary and PayPal subscribe buttons |
| `POST /api/billing/subscribe` | record a subscription approved via PayPal |
| `POST /api/billing/cancel` | cancel the current subscription with PayPal |
| `POST /webhooks/paypal` | PayPal's subscription lifecycle events |

`PAYPAL_CLIENT_ID`, `PAYPAL_SECRET` and `PAYPAL_WEBHOOK_ID` are read from the
environment. Without the webhook id, deliveries are rejected rather than
trusted unverified, so it must be set once a webhook pointed at
`/webhooks/paypal` exists in the PayPal dashboard.

There is no flag to choose a billing environment: `--dev-mode` runs against
PayPal's sandbox and `newPayPalClient` picks the API host and the pair of plan
ids together, so a development run can never bill against a live plan. The
four plan ids are constants in `editor_billing.go`. Sandbox and live are
separate PayPal accounts, so `--dev-mode` also needs sandbox credentials in
`PAYPAL_CLIENT_ID` / `PAYPAL_SECRET`; the environment in use is logged at
startup.

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
7. **Accounts and documents** — Firebase auth, Firestore documents, a document
   list, and sharing by email. *(done)*
8. Whole-bill features: declarations of intent, short titles, sunset and
   severability clauses, reporting requirements (Rule 7); definitions helpers
   (Rule 9); the Rule 6 effective-date assistant.
9. Diffing a bill against a prior version; comments and review.
10. Resolutions (Rule 10) and Construction Code conventions (Appendix A).

## 11. Files

| Path | Role |
| --- | --- |
| `editor.go` | page handlers, document API, server-side bill rendering |
| `editor_auth.go` | Firebase session cookies |
| `editor_docs.go` | the Firestore document model and access rules |
| `editor_profile.go` | display names |
| `editor_billing.go` | PayPal subscriptions, the webhook, free-plan limits |
| `editor_law.go` | the law API over nyc_code_archive |
| `templates/editor_base.html` | the editor site's chrome |
| `templates/editor_susi.html` | sign in |
| `templates/editor_documents.html` | the document list |
| `templates/editor_new.html` | new-bill prompt |
| `templates/editor_profile.html` | display name, plan summary |
| `templates/editor_billing.html` | plan and PayPal subscribe/cancel |
| `templates/drafting_manual.html` | the rules, excerpted from the 2022 manual |
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
