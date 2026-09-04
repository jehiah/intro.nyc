// Editor wiring: ProseMirror setup, the title nav, the law-section picker, the
// reference builder, style checks, export and persistence.

import { EditorState, Plugin, TextSelection } from "prosemirror-state";
import { EditorView, Decoration, DecorationSet } from "prosemirror-view";
import { keymap } from "prosemirror-keymap";
import { baseKeymap, toggleMark } from "prosemirror-commands";
import { history, undo, redo } from "prosemirror-history";

import { schema, designatorLabel } from "./schema.js";
import {
  trackedChangesPlugin,
  trackedBackspace,
  trackedDelete,
  trackedBackspaceWord,
  trackedDeleteWord,
  trackedReplace,
  writeAddition,
  markDeleted,
  restoreDeleted,
  blockStructuralEdit,
  splitAddedProvision,
  indentProvision,
  outdentProvision,
  insertLineBreak,
  contextAt,
  TRACKED,
} from "./track.js";
import {
  searchLaw,
  fetchSection,
  loadDatasets,
  textBlocks,
  buildBillSection,
  composeLeadIn,
  emptyBill,
  additionTargets,
  repealTitleClause,
} from "./corpus.js";
import { buildReference, SUBUNIT_LEVELS } from "./refs.js";
import { runChecks } from "./lint.js";
import { wireDownloadMenu } from "./exports.js";
import {
  documentID,
  canShare,
  loadLocal,
  saveLocal,
  fetchDocument,
  saveDocument,
  saveSharing,
  documentURL,
} from "./drafts.js";

let view;
const docID = documentID();

// The provision currently chosen in the picker, and its selectable blocks.
let chosenSection = null;
let chosenBlocks = [];

// Checks cite either a numbered rule or an appendix.
function ruleLabel(rule) {
  return /^\d/.test(rule) ? `Rule ${rule}` : rule;
}

// Anchors in /drafting-manual, which reproduces the rules the checks enforce.
function ruleHref(rule) {
  return "/drafting-manual#rule-" + rule.toLowerCase().replace(/[\s.]+/g, "-");
}

/* ------------------------------------------------------------------ plugins */

function lintPlugin(onUpdate) {
  const compute = (doc) => {
    const problems = runChecks(doc, schema);
    onUpdate(problems);
    return DecorationSet.create(
      doc,
      problems
        .filter((p) => p.to > p.from)
        .map((p) =>
          Decoration.inline(p.from, p.to, {
            class: "lint lint-" + p.severity,
            title: `${ruleLabel(p.rule)}: ${p.message}`,
          })
        )
    );
  };

  return new Plugin({
    state: {
      init: (_, state) => compute(state.doc),
      apply: (tr, old) => (tr.docChanged ? compute(tr.doc) : old),
    },
    props: {
      decorations(state) {
        return this.getState(state);
      },
    },
  });
}

function persistencePlugin() {
  let timer = null;
  return new Plugin({
    view() {
      return {
        update(v, prev) {
          if (v.state.doc.eq(prev.doc)) return;
          saveLocal(docID, v.state.doc.toJSON());
          clearTimeout(timer);
          timer = setTimeout(() => persist(v.state.doc), 900);
        },
      };
    },
  });
}

async function persist(doc) {
  const attrs = doc.firstChild.attrs;
  setStatus("Saving\u2026");
  try {
    await saveDocument(docID, {
      title: attrs.subject,
      code: attrs.code,
      doc: doc.toJSON(),
    });
    setStatus("");
  } catch (e) {
    console.error(e);
    // The local copy is the only thing standing between a failed save and lost
    // work, so say so plainly.
    setStatus("Not saved \u2014 kept in this browser only");
  }
}

function editorKeymap() {
  return keymap({
    "Mod-z": undo,
    "Shift-Mod-z": redo,
    "Mod-y": redo,
    Backspace: trackedBackspace,
    Delete: trackedDelete,
    "Mod-Backspace": trackedBackspaceWord,
    "Alt-Backspace": trackedBackspaceWord,
    "Mod-Delete": trackedDeleteWord,
    "Alt-Delete": trackedDeleteWord,
    "Mod-d": markDeleted,
    "Shift-Mod-d": restoreDeleted,
    // Splitting amended law text would renumber the law itself; in a provision
    // the bill is adding, Enter starts the next one.
    Enter: (state, dispatch) =>
      blockStructuralEdit(state) || splitAddedProvision(state, dispatch),
    "Shift-Enter": insertLineBreak,
    Tab: indentProvision,
    "Shift-Tab": outdentProvision,
    "Mod-b": toggleMark(schema.marks.ins),
  });
}

// Indent and outdent apply only inside a provision the bill adds, and only
// where Rule 4.3 leaves room, so the buttons follow the cursor. Each command
// reports whether it would apply when called without a dispatch.
function toolbarPlugin() {
  const sync = (state) => {
    document.getElementById("btn-indent").disabled = !indentProvision(state);
    document.getElementById("btn-outdent").disabled = !outdentProvision(state);
  };
  return new Plugin({
    view(v) {
      sync(v.state);
      return { update: (updated) => sync(updated.state) };
    },
  });
}

function createState(doc) {
  return EditorState.create({
    doc,
    plugins: [
      history(),
      editorKeymap(),
      keymap(baseKeymap),
      trackedChangesPlugin(),
      sectionPlugin(),
      toolbarPlugin(),
      lintPlugin(renderProblems),
      persistencePlugin(),
    ],
  });
}

/* --------------------------------------------------------------- title nav */

function syncTitleInputs() {
  const { code, subject } = view.state.doc.firstChild.attrs;
  document.getElementById("title-code").value = code;
  document.getElementById("title-subject").value = subject;
}

function setTitleAttrs(attrs) {
  const current = view.state.doc.firstChild.attrs;
  const tr = view.state.tr
    .setMeta(TRACKED, true)
    // Title edits are their own undo stream; they should not interleave with
    // amendments to the bill text.
    .setMeta("addToHistory", false)
    .setNodeMarkup(0, null, { ...current, ...attrs });
  view.dispatch(tr);
}

/* ------------------------------------------------------------ document edits */

function effectiveDateOffset(doc) {
  let insertAt = doc.content.size;
  doc.forEach((node, offset) => {
    if (node.type.name === "bill_section" && node.attrs.kind === "effective") {
      insertAt = offset;
    }
  });
  return insertAt;
}

// `intoText` puts the cursor in the section's last law block rather than in the
// lead-in, for a section whose text is the drafter's to write.
function insertBillSection(node, { intoText = false } = {}) {
  const at = effectiveDateOffset(view.state.doc);
  const tr = view.state.tr.setMeta(TRACKED, true).insert(at, node);
  // at + 2 is inside the lead-in; the end of the last block is two closing
  // tokens back from the end of the bill section.
  const cursor = intoText && node.childCount > 1 ? at + node.nodeSize - 2 : at + 2;
  tr.setSelection(TextSelection.create(tr.doc, cursor));
  view.dispatch(tr.scrollIntoView());
  view.focus();
}

function insertText(text) {
  const { from, to } = view.state.selection;
  const dispatch = view.dispatch.bind(view);
  const kind = contextAt(view.state, from);
  if (kind === "amend") {
    trackedReplace(view.state, dispatch, from, to, text);
  } else if (kind === "add") {
    // A reference written into a new provision is part of what the bill adds.
    writeAddition(view.state, dispatch, from, to, text);
  } else {
    view.dispatch(
      view.state.tr.setMeta(TRACKED, true).insertText(text, from, to)
    );
  }
  view.focus();
}

/* ---------------------------------------------------------------- UI: chrome */

function openModal(id) {
  document.getElementById(id).classList.add("open");
}
function closeModal(id) {
  document.getElementById(id).classList.remove("open");
}
function setStatus(text) {
  document.getElementById("editor-status").textContent = text;
}
function escapeHTML(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

/* ------------------------------------------------------- UI: section picker */

function blockLabel(section, block) {
  if (block.level === "section") {
    return `\u00a7 ${section.cite} ${section.heading}`;
  }
  const label = designatorLabel(block.level, block.designator);
  const prefix = label || `(${block.level})`;
  return `${prefix} ${block.text.slice(0, 70)}\u2026`;
}

let searchTimer = null;

function onSearchInput() {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(runSearch, 200);
}

async function runSearch() {
  const query = document.getElementById("picker-query").value.trim();
  const results = document.getElementById("picker-results");
  if (query.length < 2) {
    results.innerHTML = "";
    return;
  }
  let hits;
  try {
    hits = await searchLaw(query);
  } catch (e) {
    console.error(e);
    results.innerHTML =
      '<p class="rule-cite mb-0">Could not search the law archive.</p>';
    return;
  }
  if (!hits.length) {
    results.innerHTML = '<p class="rule-cite mb-0">No sections found.</p>';
    return;
  }

  results.innerHTML = "";
  hits.forEach((hit) => {
    const item = document.createElement("button");
    item.type = "button";
    item.className = "picker-result";
    item.innerHTML = `<span class="picker-cite">\u00a7 ${escapeHTML(
      hit.cite
    )}</span> ${escapeHTML(hit.heading)}<span class="picker-path">${escapeHTML(
      hit.path
    )}</span>`;
    item.addEventListener("click", () => chooseSection(hit));
    results.appendChild(item);
  });
}

async function chooseSection(ref) {
  let section;
  try {
    section = await fetchSection(ref);
  } catch (e) {
    console.error(e);
    return;
  }
  chosenSection = section;
  chosenBlocks = textBlocks(section);

  document.getElementById("picker-results").innerHTML = "";
  document.getElementById("picker-query").value = "";
  document.getElementById("picker-chosen").textContent = `\u00a7 ${
    section.cite
  } ${section.heading} \u2014 ${ref.path}`;

  renderPickerDetail();
}

function currentOperation() {
  return document.querySelector('input[name="picker-operation"]:checked').value;
}

// Everything below the operation radios depends on the operation \u2014 an addition
// names a container and a new designator, an amendment and a repeal name
// existing provisions \u2014 so the detail panel is rendered as a whole.
function renderPickerDetail() {
  const operation = currentOperation();
  const adding = operation === "add";
  const repealing = operation === "repeal";
  const section = chosenSection;

  // Under `add` the search is for an anchor: the provision that will contain
  // the new one, or the one it sits beside. The new provision itself is not in
  // the archive, so it cannot be searched for.
  document.getElementById("picker-query-label").textContent = adding
    ? "Find the section it goes in or next to"
    : "Find a section";

  document.getElementById("picker-detail").hidden = !section;
  if (!section) {
    updateInsertState();
    return;
  }

  document.getElementById("picker-blocks-label").textContent = repealing
    ? "Provisions to repeal"
    : "Provisions to bring into the bill";
  document.getElementById("picker-blocks-row").hidden = adding;
  document.getElementById("picker-add-row").hidden = !adding;
  document.getElementById("picker-separate-row").hidden = repealing || adding;
  document.getElementById("picker-title-row").hidden = !repealing;
  document.getElementById("picker-title-preview").hidden = !repealing;

  if (adding) {
    renderPickerContainers(section);
    updateAdditionFields();
  } else {
    renderPickerBlocks(section, repealing);
  }

  renderPickerPreview();
}

// Rule 3.1.8: the containers the anchor section offers, one of which the new
// provision is added to.
function renderPickerContainers(section) {
  const targets = additionTargets(section);
  const chosen = document.querySelector(
    'input[name="picker-container"]:checked'
  );
  const keep = targets.some((t) => t.key === (chosen && chosen.value))
    ? chosen.value
    : "section";

  const list = document.getElementById("picker-containers");
  list.innerHTML = "";
  targets.forEach((target) => {
    const id = `picker-container-${target.key}`;
    const row = document.createElement("div");
    row.className = "form-check";
    row.innerHTML = `
      <input class="form-check-input" type="radio" name="picker-container"
             value="${target.key}" id="${id}" ${
      target.key === keep ? "checked" : ""
    }>
      <label class="form-check-label" for="${id}">${escapeHTML(
      target.text.charAt(0).toUpperCase() + target.text.slice(1)
    )} \u2014 a new ${target.level}</label>`;
    list.appendChild(row);
  });
}

function selectedContainer() {
  const el = document.querySelector('input[name="picker-container"]:checked');
  if (!el || !chosenSection) return null;
  return additionTargets(chosenSection).find((t) => t.key === el.value) || null;
}

// What the new provision is called, and whether it can carry a heading, both
// follow from the container it is added to.
function updateAdditionFields() {
  const level = (selectedContainer() || {}).level || "provision";
  document.getElementById("picker-new-level").textContent = level;
  const designator = document.getElementById("picker-new-designator");
  designator.placeholder = level === "section" ? "17-514" : "c";
  // A section number is not a subdivision letter, so a designator typed for one
  // level does not carry over to another.
  if (designator.dataset.level && designator.dataset.level !== level) {
    designator.value = "";
  }
  designator.dataset.level = level;
  // Only sections carry a heading (Rule 4.2).
  document.getElementById("picker-new-heading-col").hidden =
    level !== "section";
}

function currentAddition() {
  const container = selectedContainer();
  return {
    container: container ? container.text : "",
    level: container ? container.level : "provision",
    designator: document.getElementById("picker-new-designator").value.trim(),
    heading: document.getElementById("picker-new-heading").value.trim(),
  };
}

// Insert is disabled only while something is genuinely missing, and says what.
function updateInsertState() {
  const operation = currentOperation();
  let missing = "";
  if (!chosenSection) {
    missing =
      operation === "add"
        ? "Find the section the new provision goes in or next to."
        : "Find a section to insert.";
  } else if (operation === "add" && !currentAddition().designator) {
    missing = `Enter a designator for the new ${
      (selectedContainer() || {}).level || "provision"
    }.`;
  }
  document.getElementById("btn-picker-insert").disabled = Boolean(missing);
  const hint = document.getElementById("picker-insert-hint");
  hint.textContent = missing;
  hint.hidden = !missing;
}

function renderPickerBlocks(section, repealing) {
  const list = document.getElementById("picker-blocks");
  list.innerHTML = "";

  if (repealing) {
    // Rule 11.1.4: a repeal names what it removes, so the whole section and a
    // single subunit are different acts and have to be chosen explicitly.
    const row = document.createElement("div");
    row.className = "form-check";
    row.innerHTML = `
      <input class="form-check-input" type="checkbox" id="picker-whole" checked>
      <label class="form-check-label" for="picker-whole">
        The entire section (\u00a7 ${escapeHTML(section.cite)})</label>`;
    list.appendChild(row);
  }

  chosenBlocks.forEach((block, i) => {
    const id = `picker-block-${i}`;
    const row = document.createElement("div");
    row.className = "form-check";
    row.innerHTML = `
      <input class="form-check-input" type="checkbox" value="${i}" id="${id}" ${
      !repealing && i === 0 ? "checked" : ""
    }>
      <label class="form-check-label" for="${id}">${escapeHTML(
      blockLabel(section, block)
    )}</label>`;
    list.appendChild(row);
  });

  const skipped = (section.blocks || []).length - chosenBlocks.length;
  if (skipped > 0) {
    const note = document.createElement("p");
    note.className = "rule-cite mb-0 mt-2";
    note.textContent = `${skipped} table or non-text block not shown; add it by hand.`;
    list.appendChild(note);
  }
}

function wholeSectionSelected() {
  const whole = document.getElementById("picker-whole");
  return Boolean(whole && whole.checked);
}

function selectedBlocks() {
  const operation = currentOperation();
  // An addition sets out only the new provision; the anchor's existing text is
  // not reproduced.
  if (operation === "add") return [];
  const checked = [
    ...document.querySelectorAll("#picker-blocks input[value]:checked"),
  ].map((el) => Number(el.value));
  if (operation === "repeal") {
    return checked.map((i) => chosenBlocks[i]).filter(Boolean);
  }
  const indices = checked.length ? checked : [0];
  return indices.map((i) => chosenBlocks[i]).filter(Boolean);
}

function renderPickerPreview() {
  if (!chosenSection) return;
  const operation = currentOperation();
  const blocks = selectedBlocks();
  const wholeSection = wholeSectionSelected();

  // The preview is the lead-in itself, composed by the same function that
  // writes it into the document, so the two cannot drift.
  document.getElementById("picker-preview").textContent = composeLeadIn({
    section: chosenSection,
    blocks,
    operation,
    wholeSection,
    addition: operation === "add" ? currentAddition() : null,
  });

  updateInsertState();

  if (operation === "repeal") {
    document.getElementById("picker-title-preview").textContent =
      "Title: \u2026 " +
      repealTitleClause({
        section: chosenSection,
        blocks,
        wholeSection,
        titleCode: view.state.doc.firstChild.attrs.code,
      });
  }

  const history = chosenSection.history || {};
  let note = history.note
    ? history.note
    : "No amendment history recorded \u2014 no recital required (Rule 3.1.2).";
  if (operation === "repeal") {
    note = "No recital of legislative history for a repeal (Rule 3.1.10). " + note;
  } else if (operation === "add") {
    note = "No recital of legislative history for an addition (Rule 3.1.8). " + note;
  } else if (history.repealed) {
    note = "This provision is shown as repealed. " + note;
  }
  document.getElementById("picker-history").textContent = note;
}

// Rule 2.1.1: append the repeal to the title rather than leaving the drafter to
// remember it.
function addRepealToTitle(clause) {
  const attrs = view.state.doc.firstChild.attrs;
  if (attrs.subject.includes(clause)) return;
  const subject = attrs.subject.trim();
  setTitleAttrs({
    subject: subject ? `${subject}, ${clause}` : clause,
  });
  syncTitleInputs();
}

function insertFromPicker() {
  if (!chosenSection) return;
  const operation = currentOperation();
  const separate = document.getElementById("picker-separate").checked;
  const blocks = selectedBlocks();
  const wholeSection = wholeSectionSelected();
  const section = chosenSection;

  if (operation === "repeal") {
    if (
      document.getElementById("picker-title-clause").checked
    ) {
      addRepealToTitle(
        repealTitleClause({
          section,
          blocks,
          wholeSection,
          titleCode: view.state.doc.firstChild.attrs.code,
        })
      );
    }
    insertBillSection(
      buildBillSection({ section, blocks, operation, wholeSection })
    );
  } else if (operation === "add") {
    // The new provision is empty apart from a heading, so the cursor belongs in
    // it rather than in the lead-in.
    insertBillSection(
      buildBillSection({
        section,
        blocks,
        operation,
        addition: currentAddition(),
      }),
      { intoText: true }
    );
  } else if (separate && blocks.length > 1) {
    // Rule 3.4.2: non-consecutive provisions may go in separate bill sections.
    blocks.forEach((block) =>
      insertBillSection(
        buildBillSection({ section, blocks: [block], operation })
      )
    );
  } else {
    // Rule 3.4.1: one bill section carrying the intervening text.
    insertBillSection(buildBillSection({ section, blocks, operation }));
  }

  closeModal("modal-picker");
  resetPicker();
}

function resetPicker() {
  chosenSection = null;
  chosenBlocks = [];
  document.getElementById("picker-results").innerHTML = "";
  document.getElementById("picker-query").value = "";
  document.getElementById("picker-containers").innerHTML = "";
  document.getElementById("picker-new-designator").value = "";
  document.getElementById("picker-new-heading").value = "";
  renderPickerDetail();
}

/* ---------------------------------------------------- UI: reference builder */

function currentReference() {
  const cite = document.getElementById("ref-cite").value.trim() || "___";
  const context = document.getElementById("ref-context").value;
  const chain = SUBUNIT_LEVELS.map((level) => ({
    level,
    designator: document.getElementById("ref-" + level).value.trim(),
  })).filter((u) => u.designator);

  const anchorValue = document.getElementById("ref-anchor").value;
  const anchor = anchorValue === "none" ? null : anchorValue;

  return buildReference({ chain, cite, context, anchor });
}

function renderReferencePreview() {
  document.getElementById("ref-preview").textContent = currentReference();
}

/* -------------------------------------------------------- UI: style checks */

const SEVERITY_CLASS = {
  error: "text-danger",
  warning: "text-warning-emphasis",
  info: "text-secondary",
};

function renderProblems(list) {
  const panel = document.getElementById("issues");
  if (!panel) return;
  document.getElementById("issue-count").textContent = list.length;

  if (!list.length) {
    panel.innerHTML =
      '<p class="text-secondary small mb-0">No style issues found.</p>';
    return;
  }

  panel.innerHTML = "";
  list.forEach((problem) => {
    const item = document.createElement("div");
    item.className = "issue " + (SEVERITY_CLASS[problem.severity] || "");
    item.innerHTML = `<a class="issue-rule" href="${escapeHTML(
      ruleHref(problem.rule)
    )}" target="_blank" rel="noopener">${escapeHTML(
      ruleLabel(problem.rule)
    )}</a> <button type="button" class="issue-message">${escapeHTML(
      problem.message
    )}${
      problem.excerpt
        ? ` <span class="issue-excerpt">${escapeHTML(problem.excerpt)}</span>`
        : ""
    }</button>`;
    item.querySelector(".issue-message").addEventListener("click", () => {
      if (problem.to === 0) {
        document.getElementById("title-subject").focus();
        return;
      }
      const tr = view.state.tr.setSelection(
        TextSelection.create(
          view.state.doc,
          problem.from,
          Math.min(problem.to, view.state.doc.content.size)
        )
      );
      view.dispatch(tr.scrollIntoView());
      view.focus();
    });
    panel.appendChild(item);
  });
}

/* ------------------------------------------------------------ UI: clipboard */

async function copyText(text, label) {
  await navigator.clipboard.writeText(text);
  setStatus(label);
}

/* ------------------------------------------------- bill section decorations */

// A bill section's number and its lead-in are both derived from the document —
// the number from position (Rule 3) and the lead-in from the law it operates on
// (Rule 3.1) — so neither is editable text. The number is drawn as a control
// that opens the section menu; a generated lead-in is marked non-editable.

function sectionNumberLabel(index) {
  return index === 0 ? "Section 1." : `\u00a7 ${index + 1}.`;
}

function isGeneratedLead(node) {
  return ["amend", "add", "repeal"].includes(node.attrs.kind);
}

// The bill section enclosing a position, with its bounds.
function sectionAt(pos) {
  const $pos = view.state.doc.resolve(pos);
  for (let depth = $pos.depth; depth > 0; depth--) {
    if ($pos.node(depth).type.name === "bill_section") {
      return {
        node: $pos.node(depth),
        from: $pos.before(depth),
        to: $pos.after(depth),
      };
    }
  }
  return null;
}

function sectionPill(label, getPos) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "section-pill";
  button.textContent = label;
  button.title = "Section options";
  button.contentEditable = "false";
  // mousedown rather than click so ProseMirror does not first move the
  // selection into the non-editable lead-in.
  button.addEventListener("mousedown", (e) => {
    e.preventDefault();
    openSectionMenu(getPos());
  });
  return button;
}

function sectionPlugin() {
  const compute = (doc) => {
    const decorations = [];
    let index = 0;
    doc.forEach((node, offset) => {
      if (node.type.name !== "bill_section") return;
      const label = sectionNumberLabel(index++);
      const lead = node.firstChild;
      if (!lead || lead.type.name !== "section_lead") return;

      decorations.push(
        Decoration.widget(offset + 2, (v, getPos) => sectionPill(label, getPos), {
          side: -1,
          ignoreSelection: true,
        })
      );
      if (isGeneratedLead(node)) {
        decorations.push(
          Decoration.node(offset + 1, offset + 1 + lead.nodeSize, {
            contenteditable: "false",
            class: "section-lead-generated",
          })
        );
      }
    });
    return DecorationSet.create(doc, decorations);
  };

  return new Plugin({
    state: {
      init: (_, state) => compute(state.doc),
      apply: (tr, old) => (tr.docChanged ? compute(tr.doc) : old),
    },
    props: {
      decorations(state) {
        return this.getState(state);
      },
    },
  });
}

/* ------------------------------------------------------ UI: the section menu */

let pendingSection = null;

function countSections() {
  let n = 0;
  view.state.doc.forEach((node) => {
    if (node.type.name === "bill_section") n++;
  });
  return n;
}

function sectionIndexAt(from) {
  let index = 0;
  let found = 0;
  view.state.doc.forEach((node, offset) => {
    if (node.type.name !== "bill_section") return;
    if (offset === from) found = index;
    index++;
  });
  return found;
}

// Why a section cannot be removed, or null when it can. Both the menu and the
// command consult this; a disabled button is a hint, not a guard.
function sectionRemovalBlocker(section) {
  if (section.node.attrs.kind === "effective") {
    return "A bill must state when it takes effect, so this section cannot be removed (Rule 6).";
  }
  if (countSections() < 2) {
    return "A bill needs at least one section.";
  }
  return null;
}

function openSectionMenu(pos) {
  const section = sectionAt(pos);
  if (!section) return;
  pendingSection = section;

  document.getElementById("section-number").textContent = sectionNumberLabel(
    sectionIndexAt(section.from)
  );
  document.getElementById("section-lead").textContent =
    section.node.firstChild.textContent;

  // A section that cannot be removed offers nothing to cancel, so the menu
  // becomes a single Close.
  const blocker = sectionRemovalBlocker(section);
  const remove = document.getElementById("btn-section-remove");
  const dismiss = document.getElementById("btn-section-dismiss");
  remove.hidden = Boolean(blocker);
  dismiss.textContent = blocker ? "Close" : "Cancel";
  dismiss.className = blocker ? "btn btn-primary" : "btn btn-outline-secondary";
  document.getElementById("section-note").textContent =
    blocker || "Removing this section deletes it and any law text it carries.";

  openModal("modal-section");
}

function removeSection() {
  if (!pendingSection) return;
  const from = pendingSection.from;
  pendingSection = null;
  closeModal("modal-section");

  // The document may have moved since the menu opened, and the rule is checked
  // again here rather than trusting the button state.
  const node = view.state.doc.nodeAt(from);
  if (!node || node.type.name !== "bill_section") return;
  if (sectionRemovalBlocker({ node, from })) return;

  view.dispatch(
    view.state.tr
      .setMeta(TRACKED, true)
      .delete(from, from + node.nodeSize)
      .scrollIntoView()
  );
  view.focus();
}

/* ---------------------------------------------------------------- UI: share */

// The dialog saves on every change, so it holds the current sharing state.
let sharing = { owner: "", editors: [], viewers: [], public: false, names: {} };

function shareError(message) {
  const el = document.getElementById("share-error");
  el.textContent = message || "";
  el.hidden = !message;
}

// A person reads as their name over their address, or just the address when no
// profile name is known.
function personHTML(email) {
  const name = sharing.names[email];
  if (!name) {
    return `<span class="share-person"><span class="share-email">${escapeHTML(
      email
    )}</span></span>`;
  }
  return `<span class="share-person"><span class="share-name">${escapeHTML(
    name
  )}</span><span class="share-email">${escapeHTML(email)}</span></span>`;
}

function renderShare() {
  document.getElementById("share-public").checked = Boolean(sharing.public);

  const list = document.getElementById("share-people");
  list.innerHTML = "";

  // The owner is always listed and cannot be changed or removed.
  if (sharing.owner) {
    const row = document.createElement("li");
    row.innerHTML = `${personHTML(sharing.owner)}<span class="share-role">Owner</span>`;
    list.appendChild(row);
  }

  const people = [
    ...sharing.editors.map((email) => ({ email, role: "editor" })),
    ...sharing.viewers.map((email) => ({ email, role: "viewer" })),
  ].sort((a, b) => a.email.localeCompare(b.email));

  people.forEach((person) => {
    const row = document.createElement("li");
    row.innerHTML = `
      ${personHTML(person.email)}
      <select class="form-select form-select-sm" aria-label="Access for ${escapeHTML(
        person.email
      )}">
        <option value="viewer">Viewer</option>
        <option value="editor">Editor</option>
        <option value="remove">Remove</option>
      </select>`;
    const select = row.querySelector("select");
    select.value = person.role;
    select.addEventListener("change", () => {
      setRole(person.email, select.value);
    });
    list.appendChild(row);
  });
}

function setRole(email, role) {
  sharing.editors = sharing.editors.filter((e) => e !== email);
  sharing.viewers = sharing.viewers.filter((e) => e !== email);
  if (role === "editor") sharing.editors.push(email);
  if (role === "viewer") sharing.viewers.push(email);
  renderShare();
  commitSharing();
}

async function commitSharing() {
  try {
    const saved = await saveSharing(docID, {
      editors: sharing.editors,
      viewers: sharing.viewers,
      isPublic: sharing.public,
    });
    // The server normalizes addresses and resolves duplicates, so its answer
    // is what the dialog shows.
    sharing = {
      owner: sharing.owner,
      editors: saved.editors || [],
      viewers: saved.viewers || [],
      public: Boolean(saved.public),
      names: saved.names || {},
    };
    renderShare();
    setStatus("Sharing updated");
  } catch (e) {
    console.error(e);
    shareError(e.message || "Could not update sharing.");
    // The change was rejected (e.g. a free-plan limit), so the dialog must
    // fall back to what the server actually has rather than what was
    // optimistically drawn.
    try {
      const current = await fetchDocument(docID);
      sharing = {
        owner: current.owner || "",
        editors: current.editors || [],
        viewers: current.viewers || [],
        public: Boolean(current.public),
        names: current.names || {},
      };
      renderShare();
    } catch (e2) {
      console.error(e2);
    }
  }
}

function addPerson(event) {
  event.preventDefault();
  const input = document.getElementById("share-email");
  const email = input.value.trim().toLowerCase();
  if (!email) return;
  if (!email.includes("@")) {
    shareError(`${email} is not an email address.`);
    return;
  }
  if (email === sharing.owner) {
    shareError("You already own this draft.");
    return;
  }
  shareError("");
  input.value = "";
  setRole(email, document.getElementById("share-role").value);
}

async function openShare() {
  document.getElementById("share-url").value = documentURL(docID);
  shareError("");
  try {
    const current = await fetchDocument(docID);
    sharing = {
      owner: current.owner || "",
      editors: current.editors || [],
      viewers: current.viewers || [],
      public: Boolean(current.public),
      names: current.names || {},
    };
  } catch (e) {
    console.error(e);
    shareError("Could not load the current sharing settings.");
  }
  renderShare();
  openModal("modal-share");
  document.getElementById("share-email").focus();
}

/* ----------------------------------------------------------------- bootstrap */

function wireUI() {
  document
    .getElementById("btn-insert-law")
    .addEventListener("click", () => {
      renderPickerDetail();
      openModal("modal-picker");
      document.getElementById("picker-query").focus();
    });
  document.getElementById("btn-insert-ref").addEventListener("click", () => {
    renderReferencePreview();
    openModal("modal-ref");
  });
  document.getElementById("btn-mark-deleted").addEventListener("click", () => {
    markDeleted(view.state, view.dispatch.bind(view));
    view.focus();
  });
  document.getElementById("btn-restore").addEventListener("click", () => {
    restoreDeleted(view.state, view.dispatch.bind(view));
    view.focus();
  });
  document.getElementById("btn-indent").addEventListener("click", () => {
    indentProvision(view.state, view.dispatch.bind(view));
    view.focus();
  });
  document.getElementById("btn-outdent").addEventListener("click", () => {
    outdentProvision(view.state, view.dispatch.bind(view));
    view.focus();
  });

  document
    .getElementById("title-code")
    .addEventListener("change", (e) => setTitleAttrs({ code: e.target.value }));
  document
    .getElementById("title-subject")
    .addEventListener("input", (e) =>
      setTitleAttrs({ subject: e.target.value })
    );

  wireDownloadMenu(() => view.state.doc, setStatus);

  if (canShare()) {
    document.getElementById("btn-share").addEventListener("click", openShare);
    document.getElementById("share-add").addEventListener("submit", addPerson);
    document.getElementById("share-public").addEventListener("change", (e) => {
      sharing.public = e.target.checked;
      commitSharing();
    });
    document.getElementById("btn-share-copy").addEventListener("click", () => {
      copyText(document.getElementById("share-url").value, "Link copied");
    });
  }

  document
    .getElementById("btn-section-remove")
    .addEventListener("click", removeSection);

  // Dialogs save as they go, so dismissing one is never destructive.
  document.querySelectorAll(".editor-modal").forEach((modal) => {
    modal.addEventListener("mousedown", (e) => {
      if (e.target === modal) closeModal(modal.id);
    });
  });
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    const open = document.querySelector(".editor-modal.open");
    if (open) {
      e.preventDefault();
      closeModal(open.id);
    }
  });

  document.querySelectorAll("[data-close]").forEach((el) => {
    el.addEventListener("click", () => closeModal(el.dataset.close));
  });

  document
    .getElementById("picker-query")
    .addEventListener("input", onSearchInput);
  document
    .getElementById("picker-blocks")
    .addEventListener("change", renderPickerPreview);
  document
    .querySelectorAll('input[name="picker-operation"]')
    .forEach((el) => el.addEventListener("change", renderPickerDetail));
  document.getElementById("picker-containers").addEventListener("change", () => {
    updateAdditionFields();
    renderPickerPreview();
  });
  ["picker-new-designator", "picker-new-heading"].forEach((id) => {
    document.getElementById(id).addEventListener("input", renderPickerPreview);
  });
  document
    .getElementById("btn-picker-insert")
    .addEventListener("click", insertFromPicker);

  [
    "ref-cite",
    "ref-context",
    "ref-anchor",
    ...SUBUNIT_LEVELS.map((l) => "ref-" + l),
  ].forEach((id) => {
    const el = document.getElementById(id);
    el.addEventListener("input", renderReferencePreview);
    el.addEventListener("change", renderReferencePreview);
  });
  document.getElementById("btn-ref-insert").addEventListener("click", () => {
    insertText(currentReference());
    closeModal("modal-ref");
  });
}

// The stored document is authoritative; the local copy is a fallback for when
// the last save did not reach the server.
async function initialDoc() {
  try {
    const stored = await fetchDocument(docID);
    return schema.nodeFromJSON(stored.doc);
  } catch (e) {
    console.warn(e);
  }
  const local = loadLocal(docID);
  if (local) {
    try {
      setStatus("Loaded an unsaved local copy");
      return schema.nodeFromJSON(local);
    } catch (err) {
      console.warn("could not restore local copy", err);
    }
  }
  return emptyBill();
}

async function main() {
  view = new EditorView(document.getElementById("editor"), {
    state: createState(emptyBill()),
    // bill-editable distinguishes the editing surface from the read-only view,
    // which draws its section numbers in CSS.
    attributes: { class: "bill-doc bill-editable" },
  });

  wireUI();
  syncTitleInputs();

  view.updateState(createState(await initialDoc()));
  syncTitleInputs();

  try {
    const datasets = await loadDatasets();
    // Provenance belongs next to the search that uses it: how much law is
    // searchable, and how current it is. The RCNY is excluded because a local
    // law does not amend agency rules (Rule 5.2).
    const searchable = datasets.filter((d) => d.dataset !== "rules");
    const total = searchable.reduce((n, d) => n + (d.sections || 0), 0);
    document.getElementById("picker-currency").textContent =
      `${total.toLocaleString()} sections searchable \u00b7 ` +
      searchable
        .map((d) => `${d.label}: ${d.current_through || "currency unknown"}`)
        .join(" \u00b7 ");
  } catch (e) {
    console.error(e);
    document.getElementById("picker-currency").textContent =
      "Could not reach the law archive.";
  }
}

main();
