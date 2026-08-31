// Tracked amendment engine — Rule 11.1 of the Bill Drafting Manual.
//
// Inside a bill section that amends existing law, editing does not mutate the
// text. Removing text brackets it; adding text underlines it; and a deletion is
// always kept to the left of the addition that replaces it. Text this bill
// itself added is exempt: it was never in the law, so it is removed outright.

import { Plugin, TextSelection } from "prosemirror-state";

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

// The single replacement primitive. Everything the user can do to tracked text
// — typing, deleting, pasting — routes through here.
export function trackedReplace(state, dispatch, from, to, text) {
  const { schema } = state;
  const { ins } = schema.marks;
  const kind = kindAt(state.doc, from);

  if (kind !== "amend") return false;

  let delFrom = from;
  let delTo = to;
  let addition = text;

  const word = wordExpansion(state.doc, from, to);
  if (word && isOriginal(state.doc, word.from, word.to, schema)) {
    delFrom = word.from;
    delTo = word.to;
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
    tr.replaceWith(at, at, schema.text(addition, [ins.create()]));
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

export function trackedChangesPlugin() {
  return new Plugin({
    props: {
      handleTextInput(view, from, to, text) {
        return trackedReplace(
          view.state,
          view.dispatch.bind(view),
          from,
          to,
          text
        );
      },
      handlePaste(view, event, slice) {
        const { from, to } = view.state.selection;
        if (kindAt(view.state.doc, from) !== "amend") return false;
        const text = slice.content.textBetween(0, slice.content.size, " ", "");
        return trackedReplace(
          view.state,
          view.dispatch.bind(view),
          from,
          to,
          text
        );
      },
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
