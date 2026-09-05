// Document schema for NYC Council legislation.
//
// The structure encodes the formal parts of a bill from Rule 2 of the Bill
// Drafting Manual: a title, an enacting clause, and an ordered list of bill
// sections. A bill section is unconsolidated lead-in text (Rule 3) optionally
// followed by the consolidated law text it operates on.
//
// Bill-section numbering ("Section 1.", "§ 2.", ...) is not stored. It is a
// CSS counter in editor.css, so it can never drift from document order.

import { Schema } from "prosemirror-model";

export const LEVELS = [
  "section",
  "subdivision",
  "paragraph",
  "subparagraph",
  "clause",
  "item",
];

// Rule 4.3: subdivisions and paragraphs take a period, everything below the
// paragraph level takes parentheses.
export function designatorLabel(level, designator) {
  if (!designator) return "";
  switch (level) {
    case "subdivision":
    case "paragraph":
      return designator + ".";
    case "subparagraph":
    case "clause":
    case "item":
      return "(" + designator + ")";
    default:
      return "";
  }
}

// Rule 4.3, for a section the bill adds: "a." subdivision, "1." paragraph,
// "(a)" subparagraph, "(1)" clause, "(A)" item.
const SEQUENCES = {
  subdivision: "lower",
  paragraph: "number",
  subparagraph: "lower",
  clause: "number",
  item: "upper",
};

// a…z, then aa, bb, cc — the doubling the Administrative Code uses past z.
function nthLetter(n, first) {
  return String.fromCharCode(first + (n % 26)).repeat(Math.floor(n / 26) + 1);
}

// The n-th designator of a level, counting from zero.
export function nthDesignator(level, n) {
  switch (SEQUENCES[level]) {
    case "lower":
      return nthLetter(n, 97);
    case "upper":
      return nthLetter(n, 65);
    case "number":
      return String(n + 1);
    default:
      return "";
  }
}

// Where a designator sits in its level's sequence, or -1 for one this editor
// would not have produced — a decimal or hyphenated designator carried in from
// existing law.
export function designatorIndex(level, designator) {
  const sequence = SEQUENCES[level];
  if (!designator || !sequence) return -1;
  if (sequence === "number") {
    return /^\d+$/.test(designator) ? Number(designator) - 1 : -1;
  }
  const letter = designator[0];
  const range = sequence === "lower" ? /^[a-z]$/ : /^[A-Z]$/;
  if (!range.test(letter) || designator !== letter.repeat(designator.length)) {
    return -1;
  }
  const first = sequence === "lower" ? 97 : 65;
  return (designator.length - 1) * 26 + (letter.charCodeAt(0) - first);
}

// Rule 2.1: the title states which bodies of law the bill amends and briefly
// refers to its subject. Only the subject is drafted by hand, so the two are
// held separately and composed here.
export const TITLE_PREFIXES = {
  "administrative code":
    "To amend the administrative code of the city of New York, in relation to",
  charter: "To amend the New York city charter, in relation to",
  both:
    "To amend the New York city charter and the administrative code of the " +
    "city of New York, in relation to",
  // Rule 2.1: a wholly unconsolidated law does not name the Charter or Code.
  unconsolidated: "In relation to",
};

export function titleText(attrs) {
  const prefix = TITLE_PREFIXES[attrs.code] || TITLE_PREFIXES["administrative code"];
  return `${prefix} ${attrs.subject || ""}`.trim();
}

const nodes = {
  doc: { content: "bill_title enacting_clause bill_section+" },

  // Rendered beneath a centered "A DRAFT LOCAL LAW". Edited from the toolbar
  // rather than in the document, so the required prefix cannot be mangled.
  bill_title: {
    atom: true,
    selectable: false,
    attrs: {
      code: { default: "administrative code" },
      subject: { default: "" },
    },
    parseDOM: [
      {
        tag: "p.bill-title",
        getAttrs: (dom) => ({
          code: dom.getAttribute("data-code") || "administrative code",
          subject: dom.getAttribute("data-subject") || "",
        }),
      },
    ],
    toDOM: (node) => [
      "p",
      {
        class: "bill-title",
        "data-code": node.attrs.code,
        "data-subject": node.attrs.subject,
        contenteditable: "false",
      },
      titleText(node.attrs),
    ],
  },

  // Rule 2.2. Fixed, underlined, and never part of the body.
  enacting_clause: {
    atom: true,
    selectable: false,
    parseDOM: [{ tag: "p.enacting-clause" }],
    toDOM: () => [
      "p",
      { class: "enacting-clause", contenteditable: "false" },
      "Be it enacted by the Council as follows:",
    ],
  },

  bill_section: {
    content: "section_lead law_block*",
    defining: true,
    attrs: {
      // amend | add | repeal | unconsolidated | effective
      kind: { default: "unconsolidated" },
      cite: { default: "" },
      code: { default: "" },
    },
    parseDOM: [
      {
        tag: "section.bill-section",
        getAttrs: (dom) => ({
          kind: dom.getAttribute("data-kind") || "unconsolidated",
          cite: dom.getAttribute("data-cite") || "",
          code: dom.getAttribute("data-code") || "",
        }),
      },
    ],
    toDOM: (node) => [
      "section",
      {
        class: "bill-section kind-" + node.attrs.kind,
        "data-kind": node.attrs.kind,
        "data-cite": node.attrs.cite,
        "data-code": node.attrs.code,
      },
      0,
    ],
  },

  // The unconsolidated portion of a bill section. Never underlined (Rule 3).
  section_lead: {
    content: "inline*",
    parseDOM: [{ tag: "p.section-lead" }],
    toDOM: () => ["p", { class: "section-lead" }, 0],
  },

  // Consolidated law text. The designator is a non-editable CSS prefix so a
  // drafter cannot renumber the law by accident.
  law_block: {
    content: "inline*",
    attrs: {
      level: { default: "section" },
      designator: { default: "" },
      label: { default: "" },
    },
    parseDOM: [
      {
        tag: "p.law-block",
        getAttrs: (dom) => ({
          level: dom.getAttribute("data-level") || "section",
          designator: dom.getAttribute("data-designator") || "",
          label: dom.getAttribute("data-label") || "",
        }),
      },
    ],
    toDOM: (node) => [
      "p",
      {
        class: "law-block level-" + node.attrs.level,
        "data-level": node.attrs.level,
        "data-designator": node.attrs.designator,
        "data-label": node.attrs.label,
      },
      0,
    ],
  },

  text: { group: "inline" },

  // A line break within one provision (Shift-Enter). Text that reads on its own
  // line but is not a subunit of its own — a list lead-in, a formula — would
  // otherwise have to become a designated provision to be broken.
  hard_break: {
    inline: true,
    group: "inline",
    selectable: false,
    parseDOM: [{ tag: "br" }],
    toDOM: () => ["br"],
  },
};

const marks = {
  // Rule 11.1: new language is underlined.
  ins: {
    parseDOM: [{ tag: "u.ins" }],
    toDOM: () => ["u", { class: "ins" }, 0],
  },
  // Rule 11.1: language being removed stays in the bill, in brackets. The
  // brackets are drawn by CSS so they cannot be edited away.
  del: {
    parseDOM: [{ tag: "span.del" }],
    toDOM: () => ["span", { class: "del" }, 0],
  },
  // Rule 5: a cross-reference the editor built. The words are what the bill
  // says; these attributes are how the editor finds that provision again, to
  // show its text or to link to the publisher's. They carry no legal meaning
  // and no export prints them.
  ref: {
    attrs: {
      // Where the provision lives in the law archive.
      dataset: { default: "" },
      file: { default: "" },
      cite: { default: "" },
      // source.record_id: the publisher's own id for the record.
      record: { default: "" },
    },
    // A reference is a fixed phrase; typing against either edge is not part of
    // it.
    inclusive: false,
    parseDOM: [
      {
        tag: "span.law-ref",
        getAttrs: (dom) => ({
          dataset: dom.getAttribute("data-dataset") || "",
          file: dom.getAttribute("data-file") || "",
          cite: dom.getAttribute("data-cite") || "",
          record: dom.getAttribute("data-record") || "",
        }),
      },
    ],
    toDOM: (mark) => [
      "span",
      {
        class: "law-ref",
        "data-dataset": mark.attrs.dataset,
        "data-file": mark.attrs.file,
        "data-cite": mark.attrs.cite,
        "data-record": mark.attrs.record,
      },
      0,
    ],
  },
};

export const schema = new Schema({ nodes, marks });
