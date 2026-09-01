// Live style checks against the Bill Drafting Manual.
//
// Every check names the rule it enforces so a drafter can go read it. Checks
// are pure functions of the document, which keeps them cheap to extend and
// makes them reusable outside the editor.

import { titleText } from "./schema.js";

const TEXT_CHECKS = [
  {
    rule: "6.1",
    severity: "error",
    pattern: /\bshall take effect\b/gi,
    message: 'Use "takes effect", not "shall take effect".',
  },
  {
    rule: "6.1",
    severity: "error",
    pattern: /\bafter its enactment(?: into law)?\b/gi,
    message: 'Use "after it becomes law", not "after its enactment".',
  },
  {
    rule: "11.2",
    severity: "error",
    pattern: /\bNew York City Charter\b/g,
    message: 'Write "New York city charter" — bodies of law are not capitalized.',
  },
  {
    rule: "11.2",
    severity: "error",
    pattern: /\bAdministrative Code\b/g,
    message: 'Write "administrative code of the city of New York" in lower case.',
  },
  {
    rule: "11.2",
    severity: "warning",
    pattern: /\b(?:Department|Commissioner|Council|Mayor)\b/g,
    message:
      "Agency names, offices and titles are lower case in bill text.",
  },
  {
    rule: "11.3",
    severity: "warning",
    pattern: /\.\u0020{2,}/g,
    message: "Use a single space after a period.",
  },
  {
    rule: "11.4",
    severity: "error",
    pattern: /\u00a7(?=\S)/g,
    message: "Put a space after the section symbol.",
  },
  {
    rule: "5.1.3",
    severity: "error",
    pattern: /\bsection\s+\d[\d\-.]*\s*\([a-z0-9]+\)/gi,
    message:
      'Cite subunits in a chain — "subdivision c of section 17-507", not "section 17-507(c)".',
  },
  {
    rule: "11.6",
    severity: "warning",
    pattern: /\bshall be\b/gi,
    message: 'Prefer the present tense — "is" rather than "shall be".',
  },
  {
    rule: "11.16.1",
    severity: "warning",
    pattern: /\bmay not\b/gi,
    message: 'Use "shall not" to prohibit.',
  },
  {
    rule: "11.22",
    severity: "error",
    pattern: /\b\w+n['\u2019]t\b|\b\w+['\u2019](?:s|re|ll|ve)\b/gi,
    message: "Do not use contractions.",
  },
  {
    rule: "11.12",
    severity: "error",
    pattern:
      /\b(?:handicapped|crippled|retarded|mentally\s+ill|alien|manpower|chairman|chairwoman|policeman|fireman|workman|his\s+or\s+her|he\s+or\s+she)\b/gi,
    message: "Outdated or prohibited terminology.",
  },
  {
    rule: "8.3.2",
    severity: "warning",
    pattern: /\b\d+(?:st|nd|rd|th)\b/g,
    message: "Spell out ordinals.",
  },
  {
    rule: "11.21.1",
    severity: "info",
    pattern: /[^,]\s+which\b/g,
    message:
      'Use "that" for a restrictive clause; "which" follows a comma.',
  },
  {
    rule: "11.19.1",
    severity: "info",
    pattern: /\b\w+,\s+\w+\s+and\b/g,
    message: "Check for the serial comma before the conjunction.",
  },
];

// Bracketed text is on its way out of the law, so it is not linted. It is
// blanked rather than removed so reported positions stay accurate.
function blockText(doc, node, start, delMark) {
  const chars = new Array(node.content.size).fill(" ");
  node.descendants((child, offset) => {
    if (!child.isText) return;
    const deleted = delMark.isInSet(child.marks);
    for (let i = 0; i < child.text.length; i++) {
      chars[offset + i] = deleted ? " " : child.text[i];
    }
  });
  return { text: chars.join(""), start: start + 1 };
}

function structuralChecks(doc) {
  const problems = [];
  const sections = [];
  doc.forEach((node, offset) => {
    if (node.type.name === "bill_section") sections.push({ node, offset });
  });

  const last = sections[sections.length - 1];
  if (!last || !/\btakes? effect\b/i.test(last.node.textContent)) {
    problems.push({
      from: last ? last.offset + 1 : 0,
      to: last ? last.offset + 1 : 0,
      rule: "6",
      severity: "error",
      message: "The last bill section must state the effective date.",
    });
  }

  const title = doc.firstChild;
  const titleString = title ? titleText(title.attrs) : "";
  const repeals = sections.some(
    (s) =>
      s.node.attrs.kind === "repeal" || /\bREPEALED\b/.test(s.node.textContent)
  );
  if (repeals && !/\bto repeal\b/i.test(titleString)) {
    problems.push({
      from: 0,
      to: 0,
      rule: "2.1.1",
      severity: "error",
      message:
        "A bill that repeals a provision must identify the repeal in its title.",
    });
  }
  if (repeals) {
    // Appendix F, and the two items on that checklist the editor cannot check
    // for the drafter.
    problems.push({
      from: 0,
      to: 0,
      rule: "Appendix F",
      severity: "info",
      message:
        "Search the charter and administrative code for cross-references to the repealed provision; they must be repealed or amended too.",
    });
    problems.push({
      from: 0,
      to: 0,
      rule: "Appendix F",
      severity: "info",
      message:
        "Repealing a repeal does not revive the earlier provision; text to be revived must be added as new.",
    });
  }
  if (/\.\s*$/.test(titleString)) {
    problems.push({
      from: 0,
      to: 0,
      rule: "2.1",
      severity: "warning",
      message: "Do not put a period at the end of the bill title.",
    });
  }
  if (title && !title.attrs.subject.trim()) {
    problems.push({
      from: 0,
      to: 0,
      rule: "2.1",
      severity: "warning",
      message: "The bill title does not state a subject yet.",
    });
  }

  // Rule 4.3.2: a lone subdivision "a" suggests a missing subdivision.
  for (const { node, offset } of sections) {
    const designators = [];
    node.forEach((child) => {
      if (child.type.name === "law_block" && child.attrs.level === "subdivision") {
        designators.push(child.attrs.designator);
      }
    });
    if (designators.length === 1 && designators[0] === "a") {
      problems.push({
        from: offset + 1,
        to: offset + 1,
        rule: "4.3.2",
        severity: "warning",
        message:
          "A subdivision a with no subdivision b reads as though text is missing.",
      });
    }
  }

  return problems;
}

export function runChecks(doc, schema) {
  const problems = [];
  const delMark = schema.marks.del;

  doc.descendants((node, pos) => {
    if (!node.isTextblock) return;
    const { text, start } = blockText(doc, node, pos, delMark);
    for (const check of TEXT_CHECKS) {
      check.pattern.lastIndex = 0;
      let match;
      while ((match = check.pattern.exec(text)) !== null) {
        if (match[0].length === 0) break;
        problems.push({
          from: start + match.index,
          to: start + match.index + match[0].length,
          rule: check.rule,
          severity: check.severity,
          message: check.message,
          excerpt: match[0].trim(),
        });
      }
    }
  });

  return [...problems, ...structuralChecks(doc)].sort((a, b) => a.from - b.from);
}
