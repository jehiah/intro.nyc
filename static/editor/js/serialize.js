// Export the bill in the forms a drafter actually needs.

import { titleText } from "./schema.js";

function sectionNumberLabel(index) {
  // Rule 3: spell out "Section" for the first bill section only.
  return index === 0 ? "Section 1." : `\u00a7 ${index + 1}.`;
}

function inlineRuns(node) {
  const runs = [];
  node.forEach((child) => {
    if (!child.isText) return;
    const marks = child.marks.map((m) => m.type.name);
    runs.push({
      text: child.text,
      ins: marks.includes("ins"),
      del: marks.includes("del"),
    });
  });
  return runs;
}

function collapseRuns(runs) {
  const out = [];
  for (const run of runs) {
    const last = out[out.length - 1];
    if (last && last.ins === run.ins && last.del === run.del) {
      last.text += run.text;
    } else {
      out.push({ ...run });
    }
  }
  return out;
}

function titleOf(doc) {
  return titleText(doc.firstChild.attrs);
}

// Walk every block of every bill section, handing each to `render`.
function eachBlock(doc, render) {
  const out = [];
  let index = 0;
  doc.forEach((node) => {
    if (node.type.name !== "bill_section") return;
    const i = index++;
    node.forEach((child) => out.push(render(child, i)));
    out.push("");
  });
  return out;
}

// Plain text. Additions are delimited with underscores because plain text
// cannot underline; deletions keep the manual's brackets (Rule 11.1).
export function toPlainText(doc) {
  const lines = [
    "A LOCAL LAW",
    "",
    titleOf(doc),
    "",
    "Be it enacted by the Council as follows:",
    "",
  ];

  lines.push(
    ...eachBlock(doc, (child, i) => {
      const text = collapseRuns(inlineRuns(child))
        .map((r) => (r.del ? `[${r.text}]` : r.ins ? `_${r.text}_` : r.text))
        .join("");
      if (child.type.name === "section_lead") {
        return `${sectionNumberLabel(i)} ${text}`;
      }
      const label = child.attrs.label ? child.attrs.label + " " : "";
      return `  ${label}${text}`;
    })
  );

  return lines.join("\n");
}

// Markdown, for pasting into notes, issues or memos. Markdown has no underline,
// so additions use inline HTML and deletions keep their brackets.
export function toMarkdown(doc) {
  const lines = [
    "# A LOCAL LAW",
    "",
    `**${titleOf(doc)}**`,
    "",
    "_Be it enacted by the Council as follows:_",
    "",
  ];

  lines.push(
    ...eachBlock(doc, (child, i) => {
      const text = collapseRuns(inlineRuns(child))
        .map((r) =>
          r.del ? `\\[${r.text}\\]` : r.ins ? `<u>${r.text}</u>` : r.text
        )
        .join("");
      if (child.type.name === "section_lead") {
        return `**${sectionNumberLabel(i)}** ${text}`;
      }
      const label = child.attrs.label ? `**${child.attrs.label}** ` : "";
      return `> ${label}${text}`;
    })
  );

  return lines.join("\n");
}

// The text as it would read once adopted: brackets and their contents gone,
// additions kept.
export function toAdoptedText(doc) {
  const lines = ["A LOCAL LAW", "", titleOf(doc), ""];
  lines.push(
    ...eachBlock(doc, (child, i) => {
      const text = collapseRuns(inlineRuns(child))
        .filter((r) => !r.del)
        .map((r) => r.text)
        .join("")
        .replace(/\s{2,}/g, " ")
        .trim();
      if (!text) return "";
      if (child.type.name === "section_lead") {
        return `${sectionNumberLabel(i)} ${text}`;
      }
      const label = child.attrs.label ? child.attrs.label + " " : "";
      return `  ${label}${text}`;
    })
  );
  return lines.join("\n");
}

function runHTML(runs) {
  return runs
    .map((r) => {
      const text = escapeHTML(r.text);
      if (r.del) return `[${text}]`;
      if (r.ins) return `<u>${text}</u>`;
      return text;
    })
    .join("");
}

// HTML shaped for pasting into the Legislative Division's Word template:
// Times New Roman 12pt, double-spaced justified body (Rule 2).
export function toRichText(doc) {
  const body =
    "font-family:'Times New Roman',serif;font-size:12pt;line-height:2;text-align:justify";

  const parts = [
    `<p style="${body};text-align:center">A LOCAL LAW</p>`,
    `<p style="${body}">${escapeHTML(titleOf(doc))}</p>`,
    `<p style="${body}"><u>Be it enacted by the Council as follows:</u></p>`,
  ];

  parts.push(
    ...eachBlock(doc, (child, i) => {
      const html = runHTML(collapseRuns(inlineRuns(child)));
      if (child.type.name === "section_lead") {
        return `<p style="${body}">${sectionNumberLabel(i)} ${html}</p>`;
      }
      const label = child.attrs.label ? escapeHTML(child.attrs.label) + " " : "";
      return `<p style="${body}">${label}${html}</p>`;
    }).filter(Boolean)
  );

  return `<div>${parts.join("\n")}</div>`;
}

// A standalone HTML document, for filing or printing.
export function toHTMLDocument(doc) {
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>${escapeHTML(titleOf(doc))}</title>
<style>
body{font-family:'Times New Roman',Times,serif;font-size:12pt;line-height:2;max-width:44em;margin:3em auto;padding:0 2em;text-align:justify}
u{text-decoration:underline}
</style>
</head>
<body>
${toRichText(doc)}
</body>
</html>
`;
}

function escapeHTML(s) {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}
