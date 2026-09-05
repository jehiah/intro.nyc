// Tracked amendment engine — Rule 11.1 of the Bill Drafting Manual.
//
// Inside a bill section that amends existing law, editing does not mutate the
// text. Removing text brackets it; adding text underlines it; and a deletion is
// always kept to the left of the addition that replaces it. Text this bill
// itself added is exempt: it was never in the law, so it is removed outright.

import { Plugin, TextSelection } from "prosemirror-state";

import {
  LEVELS,
  designatorLabel,
  designatorIndex,
  nthDesignator,
} from "./schema.js";

const depthOf = (level) => LEVELS.indexOf(level);

export const TRACKED = "trackedChange";

// Characters that hold a word together for Rule 11.1.1. Hyphens and
// apostrophes are included so "comparably-worded" is treated as one word.
const WORD = /[0-9A-Za-z\u2019'\-]/;

function kindAt(doc, pos) {
  if (pos < 0 || pos > doc.content.size) return null;
  const $p = doc.resolve(pos);
  for (let d = $p.depth; d > 0; d--) {
    if ($p.node(d).type.name === "law_block") {
      const section = $p.node(d - 1);
      if (section && section.type.name === "bill_section") {
        return section.attrs.kind;
      }
    }
  }
  return null;
}

export function contextAt(state, pos) {
  return kindAt(state.doc, pos);
}

function textSegments(doc, from, to) {
  const out = [];
  doc.nodesBetween(from, to, (node, pos) => {
    if (!node.isText) return;
    const start = Math.max(from, pos);
    const end = Math.min(to, pos + node.nodeSize);
    if (start < end) out.push({ from: start, to: end, marks: node.marks });
  });
  return out;
}

function everySegment(doc, from, to, fn) {
  const segments = textSegments(doc, from, to);
  return segments.length > 0 && segments.every(fn);
}

function isOriginal(doc, from, to, schema) {
  const { ins, del } = schema.marks;
  return (
    from === to ||
    everySegment(
      doc,
      from,
      to,
      (s) => !ins.isInSet(s.marks) && !del.isInSet(s.marks)
    )
  );
}

function isDeleted(doc, from, to, schema) {
  return everySegment(doc, from, to, (s) =>
    schema.marks.del.isInSet(s.marks)
  );
}

// Bracket a range instead of removing it. Text carrying `ins` is dropped, text
// already bracketed is left alone.
function bracketRange(tr, from, to, schema) {
  const { ins, del } = schema.marks;
  const segments = textSegments(tr.doc, from, to);
  for (let i = segments.length - 1; i >= 0; i--) {
    const s = segments[i];
    if (ins.isInSet(s.marks)) tr.delete(s.from, s.to);
    else if (!del.isInSet(s.marks)) tr.addMark(s.from, s.to, del.create());
  }
  return tr;
}

// Rule 11.1.1: when part of a word changes, the whole word is bracketed and the
// whole replacement word is underlined — "[workman] worker", never
// "work[man]er".
function wordExpansion(doc, from, to) {
  const $from = doc.resolve(from);
  const $to = doc.resolve(to);
  if ($from.parent !== $to.parent || !$from.parent.isTextblock) return null;

  const start = $from.start();
  const end = $from.end();
  const text = doc.textBetween(start, end, "\n", "");
  let a = from - start;
  let b = to - start;

  if (a === b) {
    // Only expand for a cursor sitting strictly inside a word.
    const inside = a > 0 && WORD.test(text[a - 1]) && b < text.length && WORD.test(text[b]);
    if (!inside) return null;
  } else if (!/[0-9A-Za-z]/.test(text.slice(a, b))) {
    // Punctuation-only ranges are bracketed as-is (Rule 11.1.3).
    return null;
  }

  const origA = a;
  const origB = b;
  while (a > 0 && WORD.test(text[a - 1])) a--;
  while (b < text.length && WORD.test(text[b])) b++;
  if (a === origA && b === origB) return null;

  return {
    from: start + a,
    to: start + b,
    prefix: text.slice(a, origA),
    suffix: text.slice(origB, b),
  };
}

// Inserted text is underlined as an addition (Rule 11.1) and may carry marks of
// its own — a cross-reference knows what it points at. Those belong to the
// reference itself, not to the letters of a word the editor had to rebuild
// around it, so the rebuilt word is written as up to three runs.
function additionRuns(schema, text, marks, prefix, suffix) {
  const ins = schema.marks.ins.create();
  const runs = [];
  if (prefix) runs.push(schema.text(prefix, [ins]));
  if (text) runs.push(schema.text(text, [ins, ...marks]));
  if (suffix) runs.push(schema.text(suffix, [ins]));
  return runs;
}

// The single replacement primitive. Everything the user can do to tracked text
// — typing, deleting, pasting — routes through here.
export function trackedReplace(state, dispatch, from, to, text, marks = []) {
  const { schema } = state;
  const kind = kindAt(state.doc, from);

  if (kind !== "amend") return false;

  let delFrom = from;
  let delTo = to;
  let addition = text;
  let prefix = "";
  let suffix = "";

  const word = wordExpansion(state.doc, from, to);
  if (word && isOriginal(state.doc, word.from, word.to, schema)) {
    delFrom = word.from;
    delTo = word.to;
    prefix = word.prefix;
    suffix = word.suffix;
    addition = word.prefix + text + word.suffix;
    // Rebuilding the word into exactly what was already there is a no-op.
    if (addition === state.doc.textBetween(word.from, word.to, "\n", "")) {
      return true;
    }
  }

  const tr = state.tr.setMeta(TRACKED, true);

  if (delFrom !== delTo) {
    // Deleting text that is already bracketed restores it rather than
    // double-bracketing; deletion stays reversible without undo.
    if (!text && isDeleted(state.doc, delFrom, delTo, schema)) {
      tr.removeMark(delFrom, delTo, schema.marks.del);
      if (dispatch) dispatch(tr.scrollIntoView());
      return true;
    }
    bracketRange(tr, delFrom, delTo, schema);
  }

  if (addition) {
    let at = tr.mapping.map(delTo, 1);
    // Rule 11.1.2: keep a space between the closing bracket of a deletion and
    // the addition that follows it. The space itself is not underlined.
    if (delFrom !== delTo) {
      const before = tr.doc.textBetween(Math.max(0, at - 1), at, "\n", "");
      if (before && !/\s/.test(before) && !/\s/.test(addition[0])) {
        // Explicitly unmarked: the separator belongs to neither the bracketed
        // text nor the underlined addition.
        tr.replaceWith(at, at, schema.text(" "));
        at += 1;
      }
    }
    tr.replaceWith(at, at, additionRuns(schema, text, marks, prefix, suffix));
    tr.setSelection(TextSelection.create(tr.doc, at + addition.length));
  } else if (delFrom !== delTo) {
    tr.setSelection(TextSelection.create(tr.doc, tr.mapping.map(delTo, 1)));
  }

  if (dispatch) dispatch(tr.scrollIntoView());
  return true;
}

function deleteCommand(dir, byWord) {
  return (state, dispatch) => {
    const sel = state.selection;
    if (kindAt(state.doc, sel.from) !== "amend") return false;

    let { from, to } = sel;
    if (sel.empty) {
      const $c = sel.$cursor;
      if (!$c) return true;
      if (dir < 0) {
        if ($c.parentOffset === 0) return true;
        const start = $c.start();
        const text = state.doc.textBetween(start, from, "\n", "");
        let n = 1;
        if (byWord) {
          n = 0;
          while (n < text.length && /\s/.test(text[text.length - 1 - n])) n++;
          while (n < text.length && WORD.test(text[text.length - 1 - n])) n++;
          n = Math.max(n, 1);
        }
        from -= n;
      } else {
        if ($c.parentOffset === $c.parent.content.size) return true;
        const text = state.doc.textBetween(to, $c.end(), "\n", "");
        let n = 1;
        if (byWord) {
          n = 0;
          while (n < text.length && WORD.test(text[n])) n++;
          while (n < text.length && /\s/.test(text[n])) n++;
          n = Math.max(n, 1);
        }
        to += n;
      }
    }
    return trackedReplace(state, dispatch, from, to, "");
  };
}

export const trackedBackspace = deleteCommand(-1, false);
export const trackedDelete = deleteCommand(1, false);
export const trackedBackspaceWord = deleteCommand(-1, true);
export const trackedDeleteWord = deleteCommand(1, true);

// Bracket the current selection without typing a replacement.
export function markDeleted(state, dispatch) {
  const { from, to } = state.selection;
  if (from === to) return false;
  if (kindAt(state.doc, from) !== "amend") return false;
  return trackedReplace(state, dispatch, from, to, "");
}

// Un-bracket the current selection.
export function restoreDeleted(state, dispatch) {
  const { from, to } = state.selection;
  if (from === to) return false;
  const tr = state.tr.setMeta(TRACKED, true);
  tr.removeMark(from, to, state.schema.marks.del);
  if (dispatch) dispatch(tr.scrollIntoView());
  return true;
}

// A structural edit inside amended law text would renumber the law itself.
export function blockStructuralEdit(state) {
  return kindAt(state.doc, state.selection.from) === "amend";
}

// Designators in a provision the bill adds are derived from position, the way
// bill-section numbers are: putting a subdivision between a and b renumbers what
// follows rather than repeating a letter (Rule 4.3). Existing law is never
// touched — its numbering belongs to the law, not to this editor.
function resequenceAdditions(state) {
  const changes = [];

  state.doc.forEach((section, offset) => {
    if (section.type.name !== "bill_section") return;
    if (section.attrs.kind !== "add") return;

    // The first provision keeps the designator the picker gave it: a bill may
    // add "a new subdivision c", and c is where the sequence starts. Deeper
    // levels always start at the beginning of their own sequence.
    let anchorLevel = null;
    let anchorStart = 0;
    const counts = new Map();

    section.forEach((child, childOffset) => {
      if (child.type.name !== "law_block") return;
      const { level, designator, label } = child.attrs;
      // The section itself is designated by its own number, not by a sequence.
      if (level === "section" || depthOf(level) < 1) return;

      // A run of a deeper level restarts under each new parent.
      for (const seen of [...counts.keys()]) {
        if (depthOf(seen) > depthOf(level)) counts.delete(seen);
      }
      if (anchorLevel === null) {
        anchorLevel = level;
        anchorStart = Math.max(designatorIndex(level, designator), 0);
      }

      const n = counts.has(level) ? counts.get(level) + 1 : 0;
      counts.set(level, n);

      const next = nthDesignator(
        level,
        (level === anchorLevel ? anchorStart : 0) + n
      );
      const nextLabel = designatorLabel(level, next);
      if (next !== designator || nextLabel !== label) {
        changes.push({
          pos: offset + 1 + childOffset,
          attrs: { ...child.attrs, designator: next, label: nextLabel },
        });
      }
    });
  });

  if (!changes.length) return null;
  // Numbering is derived, so it is not its own undo step: undo restores the
  // structure and the designators are recomputed from it.
  const tr = state.tr.setMeta(TRACKED, true).setMeta("addToHistory", false);
  changes.forEach((c) => tr.setNodeMarkup(c.pos, null, c.attrs));
  return tr;
}

// The law_block the cursor sits in, with what surrounds it, or null when the
// selection is not inside a provision the bill adds.
function addedProvisionAt(state) {
  const { $from } = state.selection;
  if (kindAt(state.doc, $from.pos) !== "add") return null;
  if ($from.parent.type.name !== "law_block") return null;
  return {
    block: $from.parent,
    pos: $from.before($from.depth),
    index: $from.index($from.depth - 1),
    section: $from.node($from.depth - 1),
  };
}

// Enter inside a provision the bill is adding starts the next one, carrying any
// text after the cursor into it. The designator is left to the resequencer, so
// inserting between two provisions renumbers rather than duplicates.
export function splitAddedProvision(state, dispatch) {
  const found = addedProvisionAt(state);
  if (!found || !state.selection.empty) return false;

  // A new section opens with its heading, so what follows it is that section's
  // first subdivision rather than a second section.
  const level =
    found.block.attrs.level === "section"
      ? "subdivision"
      : found.block.attrs.level;

  if (dispatch) {
    const tr = state.tr.setMeta(TRACKED, true);
    tr.split(state.selection.from, 1, [
      {
        type: state.schema.nodes.law_block,
        attrs: { level, designator: "", label: "" },
      },
    ]);
    dispatch(tr.scrollIntoView());
  }
  return true;
}

// Tab and Shift-Tab in a provision the bill adds. Rule 4.3's levels are a
// nesting, so a provision moves with everything nested under it — demoting
// subdivision b turns its paragraphs into subparagraphs.
export function shiftProvisionLevel(delta) {
  return (state, dispatch) => {
    const found = addedProvisionAt(state);
    if (!found) return false;
    const { block, pos, index, section } = found;

    const current = depthOf(block.attrs.level);
    // The section level is the law's own numbering; nothing moves into it.
    if (current < 1 || current + delta < 1) return false;

    if (delta > 0) {
      // A paragraph needs a subdivision to sit under: the provision above must
      // be at this level or deeper, or there is no parent to demote into.
      const previous = index > 0 ? section.child(index - 1) : null;
      if (
        !previous ||
        previous.type.name !== "law_block" ||
        depthOf(previous.attrs.level) < current
      ) {
        return false;
      }
    } else {
      // Nothing rises above the level the bill section starts at — that level
      // is what the lead-in announced.
      let floor = null;
      section.forEach((child) => {
        if (floor !== null || child.type.name !== "law_block") return;
        if (depthOf(child.attrs.level) >= 1) floor = depthOf(child.attrs.level);
      });
      if (floor !== null && current + delta < floor) return false;
    }

    // The provision and everything nested under it move together.
    const moving = [{ pos, node: block }];
    let at = pos + block.nodeSize;
    for (let i = index + 1; i < section.childCount; i++) {
      const child = section.child(i);
      if (child.type.name !== "law_block") break;
      if (depthOf(child.attrs.level) <= current) break;
      moving.push({ pos: at, node: child });
      at += child.nodeSize;
    }
    if (
      moving.some((m) => depthOf(m.node.attrs.level) + delta >= LEVELS.length)
    ) {
      return false;
    }

    if (dispatch) {
      const tr = state.tr.setMeta(TRACKED, true);
      moving.forEach((m) => {
        tr.setNodeMarkup(m.pos, null, {
          ...m.node.attrs,
          level: LEVELS[depthOf(m.node.attrs.level) + delta],
          // The resequencer designates the provision at its new level.
          designator: "",
          label: "",
        });
      });
      dispatch(tr.scrollIntoView());
    }
    return true;
  };
}

export const indentProvision = shiftProvisionLevel(1);
export const outdentProvision = shiftProvisionLevel(-1);

// Shift-Enter: a line break inside one provision the bill adds. Amended law
// reads as it stands, so a break is not offered there — it would alter the text
// the bill reproduces — and a lead-in or an effective date is a single sentence.
// The key is swallowed either way rather than falling through to Enter, which
// would start a provision the drafter did not ask for.
export function insertLineBreak(state, dispatch) {
  if (!addedProvisionAt(state)) return true;
  if (dispatch) {
    dispatch(
      state.tr
        .setMeta(TRACKED, true)
        .replaceSelectionWith(state.schema.nodes.hard_break.create())
        .scrollIntoView()
    );
  }
  return true;
}

// Rule 11.1: a wholly new provision is underlined in full, so text written into
// one carries `ins` however little of it is there already to inherit the mark
// from. kindAt only reports a kind inside a law_block, so the lead-in — which is
// never underlined (Rule 3) — is untouched.
export function writeAddition(state, dispatch, from, to, text, marks = []) {
  if (!text || kindAt(state.doc, from) !== "add") return false;
  if (dispatch) {
    const tr = state.tr
      .setMeta(TRACKED, true)
      .replaceWith(
        from,
        to,
        state.schema.text(text, [
          state.schema.marks.ins.create(),
          ...marks,
        ])
      );
    tr.setSelection(TextSelection.create(tr.doc, from + text.length));
    dispatch(tr);
  }
  return true;
}

export function trackedChangesPlugin() {
  return new Plugin({
    props: {
      handleTextInput(view, from, to, text) {
        const dispatch = view.dispatch.bind(view);
        return (
          writeAddition(view.state, dispatch, from, to, text) ||
          trackedReplace(view.state, dispatch, from, to, text)
        );
      },
      handlePaste(view, event, slice) {
        const { from, to } = view.state.selection;
        const kind = kindAt(view.state.doc, from);
        if (kind !== "amend" && kind !== "add") return false;
        const text = slice.content.textBetween(0, slice.content.size, " ", "");
        const dispatch = view.dispatch.bind(view);
        return (
          writeAddition(view.state, dispatch, from, to, text) ||
          trackedReplace(view.state, dispatch, from, to, text)
        );
      },
    },

    // Designators follow document order however the order changed — Enter,
    // Tab, a deleted provision, an undo.
    appendTransaction(trs, oldState, newState) {
      if (!trs.some((tr) => tr.docChanged)) return null;
      return resequenceAdditions(newState);
    },

    // Backstop for edits that do not come through the handlers above (cut,
    // drag, browser input events we do not intercept).
    filterTransaction(tr, state) {
      if (!tr.docChanged || tr.getMeta(TRACKED) || tr.getMeta("history$")) {
        return true;
      }
      let doc = state.doc;
      for (const step of tr.steps) {
        const { from, to } = step;
        if (
          typeof from === "number" &&
          typeof to === "number" &&
          to > from &&
          kindAt(doc, from) === "amend" &&
          !everySegment(doc, from, to, (s) =>
            state.schema.marks.ins.isInSet(s.marks)
          )
        ) {
          return false;
        }
        const result = step.apply(doc);
        if (!result.doc) break;
        doc = result.doc;
      }
      return true;
    },
  });
}
