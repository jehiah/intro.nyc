import type { Page } from "@playwright/test";

export type BillCode =
  | "administrative code"
  | "charter"
  | "both"
  | "unconsolidated";

const CODE_RADIO: Record<BillCode, string> = {
  "administrative code": "#code-admin",
  charter: "#code-charter",
  both: "#code-both",
  unconsolidated: "#code-unconsolidated",
};

// Runs the `/new` form (templates/editor_new.html) and returns the id of the
// resulting draft once the editor at `/d/{id}` has loaded.
export async function createDraft(
  page: Page,
  { code, title }: { code: BillCode; title: string }
): Promise<string> {
  await page.goto("/new");
  await page.locator(CODE_RADIO[code]).check();
  await page.locator("#title").fill(title);
  await page.getByRole("button", { name: "Create and start drafting" }).click();
  await page.waitForURL(/\/d\/[^/]+$/);
  await page.locator("#editor").waitFor();
  const match = page.url().match(/\/d\/([^/]+)$/);
  if (!match) throw new Error(`unexpected draft URL: ${page.url()}`);
  return match[1];
}

// Edits persist through a 900ms debounce (persistencePlugin, main.js) before
// the editor POSTs to /api/draft/{id}. Call this right after the last UI
// action and before reading the draft back, so the read isn't racing the
// save.
export async function waitForSave(page: Page, id: string): Promise<void> {
  await page.waitForResponse(
    (r) => r.url().endsWith(`/api/draft/${id}`) && r.request().method() === "POST"
  );
}

// The stored document, straight from the same endpoint the editor loads from
// (editor.go EditorGetDraft) — a far more reliable assertion target than
// scraping rendered DOM text, since designators/section numbers render via
// CSS generated content and never appear in textContent().
export async function fetchDraftDoc(page: Page, id: string): Promise<any> {
  const response = await page.request.get(`/api/draft/${id}`);
  if (!response.ok()) {
    throw new Error(`fetching draft ${id} failed: ${response.status()}`);
  }
  const body = await response.json();
  return body.doc;
}

// Best-effort cleanup so a run of these specs doesn't accumulate drafts under
// the test account (DELETE /api/draft/{id}, EditorDeleteDocument).
export async function deleteDraft(page: Page, id: string): Promise<void> {
  await page.request.delete(`/api/draft/${id}`).catch(() => {});
}

// Depth-first search for the first bill_section node matching `attrs`.
export function findBillSection(
  doc: any,
  attrs: Partial<{ kind: string; cite: string }>
): any {
  const sections = doc.content.filter((n: any) => n.type === "bill_section");
  return sections.find((s: any) =>
    Object.entries(attrs).every(([k, v]) => s.attrs?.[k] === v)
  );
}

// Concatenates the plain text of a node's inline content, ignoring marks —
// enough to assert on wording without caring how it is tracked.
export function textOf(node: any): string {
  if (!node) return "";
  if (node.text != null) return node.text;
  return (node.content || []).map(textOf).join("");
}

export function lawBlocks(section: any): any[] {
  return (section.content || []).filter((n: any) => n.type === "law_block");
}

// True if every text run under `node` carries `markType` (e.g. "ins") — the
// whole point of a kind="add" bill_section (EDITOR_PLAN.md §4): nothing in it
// was ever law, so everything typed carries the addition mark.
export function allTextMarked(node: any, markType: string): boolean {
  if (node.text != null) {
    return (node.marks || []).some((m: any) => m.type === markType);
  }
  return (node.content || []).every((child: any) => allTextMarked(child, markType));
}
