// Export the bill in the forms a drafter actually needs.

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

function blocksOf(doc) {
  const title = doc.firstChild;
  const sections = [];
  doc.forEach((node) => {
    if (node.type.name === "bill_section") sections.push(node);
  });
  return { title, sections };
}

// Plain text. Additions are delimited with underscores because plain text
// cannot underline; deletions keep the manual's brackets (Rule 11.1).
export function toPlainText(doc) {
  const { title, sections } = blocksOf(doc);
  const lines = ["A LOCAL LAW", "", title.textContent, "", "Be it enacted by the Council as follows:", ""];

  sections.forEach((section, i) => {
    const parts = [];
    section.forEach((child) => {
      const runs = collapseRuns(inlineRuns(child));
      const text = runs
        .map((r) => (r.del ? `[${r.text}]` : r.ins ? `_${r.text}_` : r.text))
        .join("");
      if (child.type.name === "section_lead") {
        parts.push(`${sectionNumberLabel(i)} ${text}`);
      } else {
        const label = child.attrs.label ? child.attrs.label + " " : "";
        parts.push(`  ${label}${text}`);
      }
    });
    lines.push(...parts, "");
  });

  return lines.join("\n");
}

// The text as it would read once adopted: brackets and their contents gone,
// additions kept.
export function toAdoptedText(doc) {
  const { sections } = blocksOf(doc);
  const lines = [];
  sections.forEach((section, i) => {
    section.forEach((child) => {
      const text = collapseRuns(inlineRuns(child))
        .filter((r) => !r.del)
        .map((r) => r.text)
        .join("")
        .replace(/\s{2,}/g, " ")
        .trim();
      if (!text) return;
      if (child.type.name === "section_lead") {
        lines.push(`${sectionNumberLabel(i)} ${text}`);
      } else {
        const label = child.attrs.label ? child.attrs.label + " " : "";
        lines.push(`  ${label}${text}`);
      }
    });
    lines.push("");
  });
  return lines.join("\n");
}

// HTML shaped for pasting into the Legislative Division's Word template:
// Times New Roman 12pt, double-spaced justified body (Rule 2).
export function toRichText(doc) {
  const { title, sections } = blocksOf(doc);
  const body = ["font-family:'Times New Roman',serif;font-size:12pt;line-height:2;text-align:justify"].join("");

  const runHTML = (runs) =>
    runs
      .map((r) => {
        const text = escapeHTML(r.text);
        if (r.del) return `[${text}]`;
        if (r.ins) return `<u>${text}</u>`;
        return text;
      })
      .join("");

  const parts = [
    `<p style="${body};text-align:center">A LOCAL LAW</p>`,
    `<p style="${body}">${escapeHTML(title.textContent)}</p>`,
    `<p style="${body}"><u>Be it enacted by the Council as follows:</u></p>`,
  ];

  sections.forEach((section, i) => {
    section.forEach((child) => {
      const html = runHTML(collapseRuns(inlineRuns(child)));
      if (child.type.name === "section_lead") {
        parts.push(`<p style="${body}">${sectionNumberLabel(i)} ${html}</p>`);
      } else {
        const label = child.attrs.label ? escapeHTML(child.attrs.label) + " " : "";
        parts.push(`<p style="${body}">${label}${html}</p>`);
      }
    });
  });

  return `<div>${parts.join("\n")}</div>`;
}

function escapeHTML(s) {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}
