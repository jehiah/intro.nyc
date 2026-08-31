// The corpus of existing law available to the editor, and the composition of
// the bill-section lead-in that must accompany any amendment (Rule 3.1).
//
// Today this reads a fixture; the response shape is the contract with the
// eventual legislation API. See PLAN.md section 9.

import { schema, designatorLabel } from "./schema.js";

export const CODES = {
  "administrative code": {
    full: "administrative code of the city of New York",
    short: "administrative code",
  },
  charter: {
    full: "New York city charter",
    short: "charter",
  },
};

export async function loadCorpus() {
  const response = await fetch("/editor/api/law");
  if (!response.ok) throw new Error("could not load law corpus");
  const data = await response.json();
  return data.sections || [];
}

// Rule 3.1.1: state law is cited by chapter and year, local law by number and
// year.
function lawCitation(law) {
  if (!law) return "";
  return law.state
    ? `chapter ${law.number} of the laws of ${law.year}`
    : `local law number ${law.number} for the year ${law.year}`;
}

// Rule 3.1: the recital of legislative history.
//
//   3.1.2  never amended since the 1963 Charter / 1985 Code -> no recital
//   3.1.3  added and never amended                          -> the adding law
//   3.1.4  amended                                          -> the last amendment only
//   3.1.5  added then redesignated                          -> both laws
//   3.1.6  amended then redesignated                        -> both laws
//   3.1.7  redesignated then amended                        -> the amendment only
//   3.1.8  a pure addition                                  -> no recital
//   3.1.10 a repeal                                         -> no recital
export function historyRecital(history, operation) {
  if (!history || operation === "add" || operation === "repeal") return "";

  const clauses = [];
  if (history.amended) {
    clauses.push(`as amended by ${lawCitation(history.amended)}`);
  } else if (history.added) {
    clauses.push(`as added by ${lawCitation(history.added)}`);
  }
  if (history.redesignated) {
    clauses.push(`redesignated by ${lawCitation(history.redesignated)}`);
  }
  if (!clauses.length) return "";
  return ", " + clauses.join(" and ") + ",";
}

function capitalizeFirst(s) {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

// "Subdivision a of section 21-1013" / "Section 16-497"
export function describeTarget(section, block) {
  if (!block || block.level === "section") {
    return `section ${section.cite}`;
  }
  const label = designatorLabel(block.level, block.designator).replace(
    /\.$/,
    ""
  );
  return `${block.level} ${label} of section ${section.cite}`;
}

// The unconsolidated text that introduces a bill section (Rule 3).
export function composeLeadIn({ section, block, operation, newDesignator }) {
  const code = CODES[section.code] || CODES["administrative code"];
  const target = describeTarget(section, block);
  const recital = historyRecital(section.history, operation);

  switch (operation) {
    case "amend":
      return capitalizeFirst(
        `${target} of the ${code.full}${recital} is amended to read as follows:`
      );
    case "add":
      // Rule 3.1.8: adding to existing law without amending it.
      return capitalizeFirst(
        `${target} of the ${code.full} is amended by adding a new ${
          newDesignator || "provision"
        } to read as follows:`
      );
    case "repeal":
      // Rules 3.1.10 and 11.1.4.
      return capitalizeFirst(`${target} of the ${code.full} is REPEALED.`);
    default:
      return "";
  }
}

function textNode(text, marked) {
  const marks = marked ? [schema.marks.ins.create()] : [];
  return schema.text(text, marks);
}

function lawBlockNode(section, block, marked) {
  const isSectionLevel = block.level === "section";
  const label = isSectionLevel
    ? `§ ${section.cite}`
    : designatorLabel(block.level, block.designator);
  const text = isSectionLevel && section.heading
    ? `${section.heading} ${block.text}`
    : block.text;

  return schema.nodes.law_block.create(
    {
      level: block.level,
      designator: block.designator,
      label: label,
    },
    text ? textNode(text, marked) : null
  );
}

// Build a complete bill section around a provision of existing law.
export function buildBillSection({
  section,
  blocks,
  operation,
  newDesignator,
}) {
  const lead = composeLeadIn({
    section,
    block: blocks[0],
    operation,
    newDesignator,
  });

  // Rule 11.1: text added to consolidated law is underlined in full.
  const marked = operation === "add";
  const content =
    operation === "repeal"
      ? []
      : blocks.map((b) => lawBlockNode(section, b, marked));

  return schema.nodes.bill_section.create(
    { kind: operation, cite: section.cite, code: section.code },
    [
      schema.nodes.section_lead.create(null, schema.text(lead)),
      ...content,
    ]
  );
}

// The starting document: a title, the enacting clause, and an effective date
// (Rules 2 and 6).
export function emptyBill(code = "administrative code") {
  return schema.nodes.doc.create(null, [
    schema.nodes.bill_title.create({ code, subject: "" }),
    schema.nodes.enacting_clause.create(),
    schema.nodes.bill_section.create({ kind: "effective" }, [
      schema.nodes.section_lead.create(
        null,
        schema.text("This local law takes effect immediately.")
      ),
    ]),
  ]);
}
