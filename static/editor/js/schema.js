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
};

export const schema = new Schema({ nodes, marks });
