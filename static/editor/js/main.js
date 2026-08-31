// Editor wiring: ProseMirror setup, the law-section picker, the reference
// builder, style checks and export.

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
  markDeleted,
  restoreDeleted,
  blockStructuralEdit,
  contextAt,
  TRACKED,
} from "./track.js";
import {
  loadCorpus,
  buildBillSection,
  emptyBill,
  describeTarget,
  historyRecital,
  CODES,
} from "./corpus.js";
import { buildReference, SUBUNIT_LEVELS } from "./refs.js";
import { runChecks } from "./lint.js";
import { toPlainText, toAdoptedText, toRichText } from "./serialize.js";

const DRAFT_KEY = "intro.nyc.editor.draft";

let view;
let corpus = [];
let problems = [];

/* ------------------------------------------------------------------ plugins */

function lintPlugin(onUpdate) {
  const compute = (doc) => {
    problems = runChecks(doc, schema);
    onUpdate(problems);
    return DecorationSet.create(
      doc,
      problems
        .filter((p) => p.to > p.from)
        .map((p) =>
          Decoration.inline(p.from, p.to, {
            class: "lint lint-" + p.severity,
            title: `Rule ${p.rule}: ${p.message}`,
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

function autosavePlugin() {
  let timer = null;
  return new Plugin({
    view() {
      return {
        update(v, prev) {
          if (v.state.doc.eq(prev.doc)) return;
          clearTimeout(timer);
          timer = setTimeout(() => {
            localStorage.setItem(
              DRAFT_KEY,
              JSON.stringify(v.state.doc.toJSON())
            );
            setStatus("Draft saved locally");
          }, 500);
        },
      };
    },
  });
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

/* --------------------------------------------------------------------- init */

function createState(doc) {
  return EditorState.create({
    doc,
    plugins: [
      history(),
      editorKeymap(),
      keymap(baseKeymap),
      trackedChangesPlugin(),
      lintPlugin(renderProblems),
      autosavePlugin(),
    ],
  });
}

function loadDraft() {
  const saved = localStorage.getItem(DRAFT_KEY);
  if (!saved) return emptyBill();
  try {
    return schema.nodeFromJSON(JSON.parse(saved));
  } catch (e) {
    console.warn("could not restore draft", e);
    return emptyBill();
  }
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

/* ----------------------------------------------------------------- UI: modal */

function openModal(id) {
  document.getElementById(id).classList.add("open");
}
function closeModal(id) {
  document.getElementById(id).classList.remove("open");
}

function setStatus(text) {
  document.getElementById("editor-status").textContent = text;
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

function renderPickerBlocks() {
  const cite = document.getElementById("picker-section").value;
  const section = corpus.find((s) => s.cite === cite);
  const list = document.getElementById("picker-blocks");
  list.innerHTML = "";
  if (!section) return;

  section.blocks.forEach((block, i) => {
    const id = `picker-block-${i}`;
    const row = document.createElement("div");
    row.className = "form-check";
    row.innerHTML = `
      <input class="form-check-input" type="checkbox" value="${i}" id="${id}" ${
      i === 0 ? "checked" : ""
    }>
      <label class="form-check-label" for="${id}">${escapeHTML(
      blockLabel(section, block)
    )}</label>`;
    list.appendChild(row);
  });

  renderPickerPreview();
}

function renderPickerPreview() {
  const cite = document.getElementById("picker-section").value;
  const operation = document.querySelector(
    'input[name="picker-operation"]:checked'
  ).value;
  const section = corpus.find((s) => s.cite === cite);
  const preview = document.getElementById("picker-preview");
  if (!section) {
    preview.textContent = "";
    return;
  }
  const blocks = selectedBlocks(section);
  const recital = historyRecital(section.history, operation);
  const target = describeTarget(section, blocks[0]);
  const code = CODES[section.code] || CODES["administrative code"];

  const verb =
    operation === "amend"
      ? "is amended to read as follows:"
      : operation === "repeal"
      ? "is REPEALED."
      : "is amended by adding a new provision to read as follows:";

  preview.textContent = `${target[0].toUpperCase()}${target.slice(1)} of the ${
    code.full
  }${recital} ${verb}`;

  document.getElementById("picker-history").textContent =
    section.history && section.history.note
      ? section.history.note
      : "No amendment history recorded — no recital required (Rule 3.1.2).";
}

function selectedBlocks(section) {
  const checked = [
    ...document.querySelectorAll("#picker-blocks input:checked"),
  ].map((el) => Number(el.value));
  const indices = checked.length ? checked : [0];
  return indices.map((i) => section.blocks[i]).filter(Boolean);
}

function insertFromPicker() {
  const cite = document.getElementById("picker-section").value;
  const section = corpus.find((s) => s.cite === cite);
  if (!section) return;
  const operation = document.querySelector(
    'input[name="picker-operation"]:checked'
  ).value;
  const separate = document.getElementById("picker-separate").checked;
  const blocks = selectedBlocks(section);

  // Rule 3.4: non-consecutive provisions may go in one bill section with
  // intervening context, or in separate bill sections.
  if (separate && blocks.length > 1) {
    blocks.forEach((block) =>
      insertBillSection(
        buildBillSection({ section, blocks: [block], operation })
      )
    );
  } else {
    insertBillSection(buildBillSection({ section, blocks, operation }));
  }
  closeModal("modal-picker");
}

/* ---------------------------------------------------- UI: reference builder */

function renderReferencePreview() {
  document.getElementById("ref-preview").textContent = currentReference();
}

function currentReference() {
  const cite = document.getElementById("ref-cite").value.trim() || "___";
  const context = document.getElementById("ref-context").value;
  const chain = SUBUNIT_LEVELS.map((level) => ({
    level,
    designator: document
      .getElementById("ref-" + level)
      .value.trim(),
  })).filter((u) => u.designator);

  const anchorValue = document.getElementById("ref-anchor").value;
  const anchor = anchorValue === "none" ? null : anchorValue;

  return buildReference({ chain, cite, context, anchor });
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
    item.innerHTML = `<span class="issue-rule">Rule ${escapeHTML(
      problem.rule
    )}</span> ${escapeHTML(problem.message)}${
      problem.excerpt
        ? ` <span class="issue-excerpt">${escapeHTML(problem.excerpt)}</span>`
        : ""
    }`;
    item.addEventListener("click", () => {
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
    setStatus("Copied — paste into the Legislative Division template");
  } catch (e) {
    await navigator.clipboard.writeText(text);
    setStatus("Copied as plain text");
  }
}

/* ----------------------------------------------------------------- bootstrap */

function escapeHTML(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

function wireUI() {
  document
    .getElementById("btn-insert-law")
    .addEventListener("click", () => openModal("modal-picker"));
  document
    .getElementById("btn-insert-ref")
    .addEventListener("click", () => {
      renderReferencePreview();
      openModal("modal-ref");
    });
  document
    .getElementById("btn-mark-deleted")
    .addEventListener("click", () => {
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
    if (!confirm("Discard the current draft and start a new bill?")) return;
    localStorage.removeItem(DRAFT_KEY);
    view.updateState(createState(emptyBill()));
    setStatus("New bill");
  });

  document
    .getElementById("btn-copy-rich")
    .addEventListener("click", copyRichText);
  document.getElementById("btn-export-txt").addEventListener("click", () => {
    download("bill.txt", toPlainText(view.state.doc));
  });
  document
    .getElementById("btn-export-adopted")
    .addEventListener("click", () => {
      download("bill-as-adopted.txt", toAdoptedText(view.state.doc));
    });
  document.getElementById("btn-export-json").addEventListener("click", () => {
    download(
      "bill.json",
      JSON.stringify(view.state.doc.toJSON(), null, 2),
      "application/json"
    );
  });

  document.querySelectorAll("[data-close]").forEach((el) => {
    el.addEventListener("click", () => closeModal(el.dataset.close));
  });

  document
    .getElementById("picker-section")
    .addEventListener("change", renderPickerBlocks);
  document
    .getElementById("picker-blocks")
    .addEventListener("change", renderPickerPreview);
  document
    .querySelectorAll('input[name="picker-operation"]')
    .forEach((el) => el.addEventListener("change", renderPickerPreview));
  document
    .getElementById("btn-picker-insert")
    .addEventListener("click", insertFromPicker);

  ["ref-cite", "ref-context", "ref-anchor", ...SUBUNIT_LEVELS.map((l) => "ref-" + l)]
    .forEach((id) =>
      document.getElementById(id).addEventListener("input", renderReferencePreview)
    );
  document
    .getElementById("ref-context")
    .addEventListener("change", renderReferencePreview);
  document.getElementById("btn-ref-insert").addEventListener("click", () => {
    insertText(currentReference());
    closeModal("modal-ref");
  });
}

async function main() {
  view = new EditorView(document.getElementById("editor"), {
    state: createState(loadDraft()),
  });

  wireUI();

  try {
    corpus = await loadCorpus();
    const select = document.getElementById("picker-section");
    select.innerHTML = corpus
      .map(
        (s) =>
          `<option value="${escapeHTML(s.cite)}">\u00a7 ${escapeHTML(
            s.cite
          )} ${escapeHTML(s.heading)}</option>`
      )
      .join("");
    renderPickerBlocks();
    setStatus(`${corpus.length} sections of law available`);
  } catch (e) {
    console.error(e);
    setStatus("Could not load the law corpus");
  }
}

main();
