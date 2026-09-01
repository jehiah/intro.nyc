// Editor wiring: ProseMirror setup, the title nav, the law-section picker, the
// reference builder, style checks, export and persistence.

import { EditorState, Plugin, TextSelection } from "prosemirror-state";
import { EditorView, Decoration, DecorationSet } from "prosemirror-view";
import { keymap } from "prosemirror-keymap";
import { baseKeymap, toggleMark } from "prosemirror-commands";
import { history, undo, redo } from "prosemirror-history";

import { schema, designatorLabel, titleText } from "./schema.js";
import {
  trackedChangesPlugin,
  trackedBackspace,
  trackedDelete,
  trackedBackspaceWord,
  trackedDeleteWord,
  trackedReplace,
  markDeleted,
  restoreDeleted,
  blockStructuralEdit,
  contextAt,
  TRACKED,
} from "./track.js";
import {
  searchLaw,
  fetchSection,
  loadDatasets,
  textBlocks,
  buildBillSection,
  emptyBill,
  describeTarget,
  describeRepealTargets,
  repealTitleClause,
  historyRecital,
  CODES,
} from "./corpus.js";
import { buildReference, SUBUNIT_LEVELS } from "./refs.js";
import { runChecks } from "./lint.js";
import {
  toPlainText,
  toMarkdown,
  toAdoptedText,
  toRichText,
  toHTMLDocument,
} from "./serialize.js";
import {
  loadLocal,
  saveLocal,
  clearLocal,
  loadHandle,
  saveDraft,
  fetchDraft,
  readOnlyURL,
} from "./drafts.js";

let view;
let handle = loadHandle();

// The provision currently chosen in the picker, and its selectable blocks.
let chosenSection = null;
let chosenBlocks = [];

// Checks cite either a numbered rule or an appendix.
function ruleLabel(rule) {
  return /^\d/.test(rule) ? `Rule ${rule}` : rule;
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
          saveLocal(v.state.doc.toJSON());
          clearTimeout(timer);
          timer = setTimeout(() => persist(v.state.doc), 900);
        },
      };
    },
  });
}

async function persist(doc) {
  setStatus("Saving\u2026");
  try {
    handle = await saveDraft(handle, titleText(doc.firstChild.attrs), doc.toJSON());
    setStatus("Saved");
  } catch (e) {
    console.error(e);
    setStatus("Saved in this browser only");
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
    // Splitting amended law text would renumber the law itself.
    Enter: (state) => blockStructuralEdit(state),
    "Mod-b": toggleMark(schema.marks.ins),
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

function insertBillSection(node) {
  const at = effectiveDateOffset(view.state.doc);
  const tr = view.state.tr.setMeta(TRACKED, true).insert(at, node);
  tr.setSelection(TextSelection.create(tr.doc, at + 2));
  view.dispatch(tr.scrollIntoView());
  view.focus();
}

function insertText(text) {
  const { from, to } = view.state.selection;
  if (contextAt(view.state, from) === "amend") {
    trackedReplace(view.state, view.dispatch.bind(view), from, to, text);
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
  document.getElementById("picker-detail").hidden = false;
  document.getElementById("btn-picker-insert").disabled = false;
  document.getElementById("picker-chosen").textContent = `\u00a7 ${
    section.cite
  } ${section.heading} \u2014 ${ref.path}`;

  renderPickerBlocks();
}

function currentOperation() {
  return document.querySelector('input[name="picker-operation"]:checked').value;
}

// The provision list, the options beside it and the preview all depend on the
// operation, so they are rendered together.
function renderPickerBlocks() {
  const section = chosenSection;
  if (!section) return;
  const operation = currentOperation();
  const repealing = operation === "repeal";

  document.getElementById("picker-blocks-label").textContent = repealing
    ? "Provisions to repeal"
    : "Provisions to bring into the bill";
  document.getElementById("picker-separate-row").hidden = repealing;
  document.getElementById("picker-title-row").hidden = !repealing;
  document.getElementById("picker-title-preview").hidden = !repealing;

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

  renderPickerPreview();
}

function wholeSectionSelected() {
  const whole = document.getElementById("picker-whole");
  return Boolean(whole && whole.checked);
}

function selectedBlocks() {
  const checked = [
    ...document.querySelectorAll("#picker-blocks input[value]:checked"),
  ].map((el) => Number(el.value));
  if (currentOperation() === "repeal") {
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
  const recital = historyRecital(chosenSection.history, operation);
  const code = CODES[chosenSection.code] || CODES["administrative code"];

  let text;
  if (operation === "repeal") {
    const target = describeRepealTargets(chosenSection, blocks, wholeSection);
    text = `${target.text} of the ${code.full} ${
      target.plural ? "are" : "is"
    } REPEALED.`;
  } else {
    const target = describeTarget(chosenSection, blocks[0]);
    const verb =
      operation === "amend"
        ? "is amended to read as follows:"
        : "is amended by adding a new provision to read as follows:";
    text = `${target} of the ${code.full}${recital} ${verb}`;
  }
  document.getElementById("picker-preview").textContent =
    text.charAt(0).toUpperCase() + text.slice(1);

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
  document.getElementById("picker-detail").hidden = true;
  document.getElementById("btn-picker-insert").disabled = true;
  document.getElementById("picker-results").innerHTML = "";
  document.getElementById("picker-query").value = "";
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
    const item = document.createElement("button");
    item.type = "button";
    item.className = "issue " + (SEVERITY_CLASS[problem.severity] || "");
    item.innerHTML = `<span class="issue-rule">${escapeHTML(
      ruleLabel(problem.rule)
    )}</span> ${escapeHTML(problem.message)}${
      problem.excerpt
        ? ` <span class="issue-excerpt">${escapeHTML(problem.excerpt)}</span>`
        : ""
    }`;
    item.addEventListener("click", () => {
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

/* --------------------------------------------------------------- UI: export */

function download(filename, text, type = "text/plain") {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

async function copyText(text, label) {
  await navigator.clipboard.writeText(text);
  setStatus(label);
}

async function copyRichText() {
  const html = toRichText(view.state.doc);
  const text = toPlainText(view.state.doc);
  try {
    await navigator.clipboard.write([
      new ClipboardItem({
        "text/html": new Blob([html], { type: "text/html" }),
        "text/plain": new Blob([text], { type: "text/plain" }),
      }),
    ]);
    setStatus("Copied \u2014 paste into the Legislative Division template");
  } catch (e) {
    await copyText(text, "Copied as plain text");
  }
}

const EXPORTS = {
  "copy-rich": copyRichText,
  "copy-text": () => copyText(toPlainText(view.state.doc), "Copied bill text"),
  "download-text": () => download("bill.txt", toPlainText(view.state.doc)),
  "copy-markdown": () =>
    copyText(toMarkdown(view.state.doc), "Copied markdown"),
  "download-markdown": () =>
    download("bill.md", toMarkdown(view.state.doc), "text/markdown"),
  "download-html": () =>
    download("bill.html", toHTMLDocument(view.state.doc), "text/html"),
  "download-adopted": () =>
    download("bill-as-adopted.txt", toAdoptedText(view.state.doc)),
  "download-json": () =>
    download(
      "bill.json",
      JSON.stringify(view.state.doc.toJSON(), null, 2),
      "application/json"
    ),
};

/* ---------------------------------------------------------------- UI: share */

async function share() {
  setStatus("Saving\u2026");
  try {
    await persist(view.state.doc);
  } catch (e) {
    /* persist reports its own status */
  }
  if (!handle) {
    setStatus("Could not create a share link");
    return;
  }
  const url = readOnlyURL(handle.id);
  document.getElementById("share-url").value = url;
  document.getElementById("share-open").href = url;
  openModal("modal-share");
}

/* ----------------------------------------------------------------- bootstrap */

function wireUI() {
  document
    .getElementById("btn-insert-law")
    .addEventListener("click", () => {
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
  document.getElementById("btn-undo").addEventListener("click", () => {
    undo(view.state, view.dispatch.bind(view));
    view.focus();
  });
  document.getElementById("btn-redo").addEventListener("click", () => {
    redo(view.state, view.dispatch.bind(view));
    view.focus();
  });
  document.getElementById("btn-new").addEventListener("click", () => {
    if (!confirm("Start a new bill? The current draft link stays available.")) {
      return;
    }
    clearLocal();
    handle = null;
    view.updateState(createState(emptyBill()));
    syncTitleInputs();
    setStatus("New bill");
  });

  document
    .getElementById("title-code")
    .addEventListener("change", (e) => setTitleAttrs({ code: e.target.value }));
  document
    .getElementById("title-subject")
    .addEventListener("input", (e) =>
      setTitleAttrs({ subject: e.target.value })
    );

  // Download menu
  const menu = document.getElementById("download-menu");
  const menuButton = document.getElementById("btn-download");
  menuButton.addEventListener("click", (e) => {
    e.stopPropagation();
    const open = menu.classList.toggle("open");
    menuButton.setAttribute("aria-expanded", String(open));
  });
  document.addEventListener("click", () => {
    menu.classList.remove("open");
    menuButton.setAttribute("aria-expanded", "false");
  });
  menu.addEventListener("click", (e) => {
    const action = e.target.dataset && e.target.dataset.export;
    if (!action) return;
    menu.classList.remove("open");
    EXPORTS[action]();
  });

  document.getElementById("btn-share").addEventListener("click", share);
  document.getElementById("btn-share-copy").addEventListener("click", () => {
    copyText(document.getElementById("share-url").value, "Share link copied");
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
    .forEach((el) => el.addEventListener("change", renderPickerBlocks));
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

// A draft named in the URL wins; otherwise resume this browser's draft.
async function initialDoc() {
  const requested = new URLSearchParams(location.search).get("d");
  const id = requested || (handle && handle.id);
  if (id) {
    try {
      const draft = await fetchDraft(id);
      if (requested && (!handle || handle.id !== requested)) {
        // Opened someone else's draft: it can be edited locally and saved as a
        // new draft, but not written back over theirs.
        handle = null;
      }
      return schema.nodeFromJSON(draft.doc);
    } catch (e) {
      console.warn(e);
    }
  }
  const local = loadLocal();
  if (local) {
    try {
      return schema.nodeFromJSON(local);
    } catch (e) {
      console.warn("could not restore local draft", e);
    }
  }
  return emptyBill();
}

async function main() {
  view = new EditorView(document.getElementById("editor"), {
    state: createState(emptyBill()),
    attributes: { class: "bill-doc" },
  });

  wireUI();
  syncTitleInputs();

  view.updateState(createState(await initialDoc()));
  syncTitleInputs();

  try {
    const datasets = await loadDatasets();
    // Provenance matters: a drafter needs to know how current the text is.
    document.getElementById("picker-currency").textContent = datasets
      .filter((d) => d.dataset !== "rules")
      .map((d) => `${d.label}: ${d.current_through || "currency unknown"}`)
      .join(" \u00b7 ");
    const total = datasets.reduce((n, d) => n + (d.sections || 0), 0);
    setStatus(`${total.toLocaleString()} sections of law available`);
  } catch (e) {
    console.error(e);
    setStatus("Could not reach the law archive");
  }
}

main();
