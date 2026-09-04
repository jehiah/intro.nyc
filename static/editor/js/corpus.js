// Existing law, and the composition of the bill-section lead-in that must
// accompany any amendment (Rule 3.1).
//
// Law comes from the nyc_code_archive: /api/law/search finds a provision and
// /api/law/section/{dataset}/{file} returns it.

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
  rules: {
    full: "rules of the city of New York",
    short: "rules",
  },
};

export async function searchLaw(query, datasets) {
  const params = new URLSearchParams({ q: query });
  (datasets || []).forEach((d) => params.append("dataset", d));
  const response = await fetch(`/api/law/search?${params}`);
  if (!response.ok) throw new Error("law search failed");
  return (await response.json()).results || [];
}

export async function fetchSection(ref) {
  const response = await fetch(
    `/api/law/section/${ref.dataset}/${ref.file}`
  );
  if (!response.ok) throw new Error(`could not load section ${ref.cite}`);
  return response.json();
}

export async function loadDatasets() {
  const response = await fetch("/api/law/datasets");
  if (!response.ok) throw new Error("could not load law datasets");
  return (await response.json()).datasets || [];
}

// Only text carries into a bill; the archive also holds tables and publisher
// apparatus.
export function textBlocks(section) {
  return (section.blocks || []).filter((b) => !b.type && b.text);
}

// Rule 3.1.1: state law is cited by chapter and year, local law by number and
// year.
function lawCitation(law) {
  if (!law) return "";
  const state = law.state || law.type === "state law";
  return state
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

const LEVEL_PLURALS = {
  section: "sections",
  subdivision: "subdivisions",
  paragraph: "paragraphs",
  subparagraph: "subparagraphs",
  clause: "clauses",
  item: "items",
};

// "a, b and c" — the manual lists repealed provisions without a serial comma
// ("Sections 3, 4, 5, 6, 7, 8 and 9 ... are REPEALED").
function joinList(parts) {
  if (parts.length <= 1) return parts[0] || "";
  if (parts.length === 2) return `${parts[0]} and ${parts[1]}`;
  return `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`;
}

// Name the provisions being repealed (Rule 11.1.4). Repealing several subunits
// of one level collapses to a plural — "subdivisions b and c of section 21-1013"
// — and the caller needs to know whether the verb agrees as singular or plural.
export function describeRepealTargets(section, blocks, wholeSection) {
  const whole = { text: `section ${section.cite}`, plural: false };
  if (wholeSection || !blocks || !blocks.length) return whole;

  const units = blocks
    .map((b) => ({
      level: b.level,
      label: designatorLabel(b.level, b.designator).replace(/\.$/, ""),
    }))
    .filter((u) => u.label && u.level !== "section");
  if (!units.length) return whole;

  const levels = new Set(units.map((u) => u.level));
  if (levels.size === 1 && units.length > 1) {
    const level = units[0].level;
    return {
      text: `${LEVEL_PLURALS[level] || level + "s"} ${joinList(
        units.map((u) => u.label)
      )} of section ${section.cite}`,
      plural: true,
    };
  }
  return {
    text: `${joinList(units.map((u) => `${u.level} ${u.label}`))} of section ${
      section.cite
    }`,
    plural: units.length > 1,
  };
}

// Rule 2.1.1: the title of a bill that repeals must identify and describe the
// provision being repealed.
export function repealTitleClause({
  section,
  blocks,
  wholeSection,
  titleCode,
}) {
  const target = describeRepealTargets(section, blocks, wholeSection);
  const code = CODES[section.code] || CODES["administrative code"];
  // "such code" only reads correctly when the title already names that body of
  // law by itself.
  const body =
    titleCode === section.code
      ? section.code === "charter"
        ? "such charter"
        : "such code"
      : `the ${code.full}`;
  const heading = (section.heading || "").replace(/\.\s*$/, "");
  const relating = heading
    ? `, relating to ${heading.charAt(0).toLowerCase()}${heading.slice(1)}`
    : "";
  return `and to repeal ${target.text} of ${body}${relating}`;
}

// Rule 4.3: what a new provision is called depends on what it is added to.
const CHILD_LEVEL = {
  section: "subdivision",
  subdivision: "paragraph",
  paragraph: "subparagraph",
  subparagraph: "clause",
  clause: "item",
};

// "chapter 5 of title 17" — the path the archive files a section under, read
// from the inside out.
function describePath(path) {
  const parts = (path || []).filter((p) => p.designator);
  if (!parts.length) return "";
  return parts
    .slice()
    .reverse()
    .map((p) => `${p.level} ${p.designator}`)
    .join(" of ");
}

// Rule 3.1.8: an addition names what it is added *to*, not an existing
// provision it sits beside, so the section the drafter searched for is an
// anchor rather than the target. Its chapter takes a new section, the section
// itself takes a new subdivision, and each subunit takes the level below it.
export function additionTargets(section) {
  if (!section) return [];
  const targets = [];
  const path = describePath(section.path);
  if (path) targets.push({ key: "path", text: path, level: "section" });
  targets.push({
    key: "section",
    text: `section ${section.cite}`,
    level: "subdivision",
  });
  textBlocks(section).forEach((block, i) => {
    const child = CHILD_LEVEL[block.level];
    if (!child || !block.designator) return;
    targets.push({
      key: `block-${i}`,
      text: describeTarget(section, block),
      level: child,
    });
  });
  return targets;
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
  if (!label) return `section ${section.cite}`;
  return `${block.level} ${label} of section ${section.cite}`;
}

// The unconsolidated text that introduces a bill section (Rule 3).
export function composeLeadIn({
  section,
  blocks,
  operation,
  wholeSection,
  addition,
}) {
  const code = CODES[section.code] || CODES["administrative code"];
  const block = (blocks || [])[0];
  const target = describeTarget(section, block);
  const recital = historyRecital(section.history, operation);

  switch (operation) {
    case "amend":
      return capitalizeFirst(
        `${target} of the ${code.full}${recital} is amended to read as follows:`
      );
    case "add": {
      // Rule 3.1.8: adding to existing law without amending it. The new
      // provision is named by its designator — "a new subdivision c", "a new
      // section 17-514" — so both the container and the designator come from
      // the drafter rather than from the anchor section.
      const { container, level, designator } = addition || {};
      const where = container ? container : `section ${section.cite}`;
      const named = [level || "provision", designator].filter(Boolean).join(" ");
      return capitalizeFirst(
        `${where} of the ${code.full} is amended by adding a new ${named} to read as follows:`
      );
    }
    case "repeal": {
      // Rules 3.1.10 and 11.1.4: no recital, and REPEALED in capitals.
      const repealed = describeRepealTargets(section, blocks, wholeSection);
      return capitalizeFirst(
        `${repealed.text} of the ${code.full} ${
          repealed.plural ? "are" : "is"
        } REPEALED.`
      );
    }
    default:
      return "";
  }
}

function textNode(text, marked) {
  const marks = marked ? [schema.marks.ins.create()] : [];
  return schema.text(text, marks);
}

// Existing law, reproduced verbatim and unmarked; the tracked engine brackets
// and underlines it as it is amended.
function lawBlockNode(section, block) {
  const isSectionLevel = block.level === "section";
  const label = isSectionLevel
    ? `\u00a7 ${section.cite}`
    : designatorLabel(block.level, block.designator);
  const text =
    isSectionLevel && section.heading
      ? `${section.heading} ${block.text}`
      : block.text;

  return schema.nodes.law_block.create(
    {
      level: block.level,
      designator: block.designator || "",
      label: label,
    },
    text ? textNode(text, false) : null
  );
}

// The empty law block a wholly new provision is drafted into. Its text is not
// in the law yet, so everything typed into it carries `ins` (Rule 11.1); a new
// section opens with its heading, which is all the editor can supply.
function additionNode(addition) {
  const { level, designator, heading } = addition;
  const label =
    level === "section"
      ? `§ ${designator}`
      : designatorLabel(level, designator);
  const text = level === "section" && heading ? heading : "";
  return schema.nodes.law_block.create(
    { level, designator, label },
    text ? textNode(text, true) : null
  );
}

// Build a complete bill section around a provision of existing law.
export function buildBillSection({
  section,
  blocks,
  operation,
  wholeSection,
  addition,
}) {
  const lead = composeLeadIn({
    section,
    blocks,
    operation,
    wholeSection,
    addition,
  });

  // Rule 11.1.4: a repeal states the provision and stops; it does not set out
  // the text being removed. An addition sets out only the new provision — the
  // existing text around it is not reproduced, because it is not being amended.
  let content = [];
  if (operation === "add") {
    content = [additionNode(addition)];
  } else if (operation !== "repeal") {
    content = blocks.map((b) => lawBlockNode(section, b));
  }

  const cite =
    operation === "add" && addition.level === "section"
      ? addition.designator
      : section.cite;

  return schema.nodes.bill_section.create(
    { kind: operation, cite, code: section.code },
    [schema.nodes.section_lead.create(null, schema.text(lead)), ...content]
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
