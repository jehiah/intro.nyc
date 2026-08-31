// Cross-reference construction — Rule 5 of the Bill Drafting Manual.
//
// References are generated rather than typed so they cannot drift from the
// required grammar: the chain runs from the smallest unit up to the section,
// subunits below the paragraph level take parentheses, and the body of law is
// named according to where the reference will live.

import { designatorLabel } from "./schema.js";

export const SUBUNIT_LEVELS = [
  "subdivision",
  "paragraph",
  "subparagraph",
  "clause",
  "item",
];

// Rule 4.3.4: use parentheses if the designator is parenthesized in the text
// being referred to, and drop a trailing period.
function referenceLabel(level, designator) {
  return designatorLabel(level, designator).replace(/\.$/, "");
}

// Rules 5.1.1-5.1.4. `context` is where the reference is being written:
//   same           - same body of consolidated law -> bare section number
//   charter        - referring into the Charter from the Administrative Code
//   administrative - referring into the Administrative Code from the Charter
//   unconsolidated - full name of the body of law
function sectionPhrase(cite, context) {
  switch (context) {
    case "charter":
      return `section ${cite} of the charter`;
    case "administrative":
      return `section ${cite} of the administrative code`;
    case "unconsolidated-charter":
      return `section ${cite} of the New York city charter`;
    case "unconsolidated-administrative":
      return `section ${cite} of the administrative code of the city of New York`;
    default:
      return `section ${cite}`;
  }
}

// chain is ordered outermost-first, e.g.
//   [{level:"subdivision",designator:"a"},{level:"paragraph",designator:"3"}]
// and is rendered innermost-first per Rule 5.1.3.
export function buildReference({ chain = [], cite, context = "same", anchor }) {
  const parts = chain
    .filter((unit) => unit.designator)
    .map((unit) => `${unit.level} ${referenceLabel(unit.level, unit.designator)}`)
    .reverse();

  // Rule 5.1.3: a reference within the same section may stop at a common
  // ancestor instead of naming the section.
  const tail = anchor ? anchor : sectionPhrase(cite, context);
  return [...parts, tail].join(" of ");
}

// Rule 5.2 / 5.4 / 5.6: rules and regulations are cited with their subject and
// an express reference to successor provisions, because agencies renumber them.
export function buildRuleReference({ source, cite, title, subject }) {
  const body = {
    "city rules": `rules of the city of New York`,
    nycrr: `New York codes, rules and regulations`,
    cfr: `code of federal regulations`,
  }[source];
  const head = title
    ? `section ${cite} of title ${title} of the ${body}`
    : `section ${cite} of the ${body}`;
  return subject ? `${head}, regarding ${subject}, or a successor provision` : head;
}

// Rule 5.3: state statutes are cited by the name of the body of state law.
export function buildStateReference({ chain = [], cite, law }) {
  const parts = chain
    .filter((unit) => unit.designator)
    .map((unit) => `${unit.level} ${referenceLabel(unit.level, unit.designator)}`)
    .reverse();
  return [...parts, `section ${cite} of the ${law}`].join(" of ");
}

// Rule 5.5: federal statutes use a different subunit vocabulary (subsection,
// paragraph, subparagraph, clause, subclause).
export function buildFederalReference({ chain = [], cite, title }) {
  const parts = chain
    .filter((unit) => unit.designator)
    .map((unit) => `${unit.level} (${unit.designator})`)
    .reverse();
  return [
    ...parts,
    `section ${cite} of title ${title} of the United States code`,
  ].join(" of ");
}

const ORDINAL_WORDS = [
  "",
  "one",
  "two",
  "three",
  "four",
  "five",
  "six",
  "seven",
  "eight",
  "nine",
  "ten",
  "eleven",
  "twelve",
];

// Rule 8.3.4: references to bill sections within the same bill are spelled out.
export function billSectionReference(n) {
  const word = ORDINAL_WORDS[n];
  return word
    ? `section ${word} of this local law`
    : `section ${n} of this local law`;
}
