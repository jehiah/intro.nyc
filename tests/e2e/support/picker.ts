import type { Page } from "@playwright/test";

// Wraps `#modal-picker` (templates/editor.html, wired in static/editor/js/main.js)
// — the flow the drafter uses to pull a real provision from the law archive
// into the bill, as an amendment, an addition, or a repeal (EDITOR_PLAN.md §5).

export type Operation = "amend" | "add" | "repeal";

const OPERATION_RADIO: Record<Operation, string> = {
  amend: "#op-amend",
  add: "#op-add",
  repeal: "#op-repeal",
};

export async function openPicker(page: Page): Promise<void> {
  await page.locator("#btn-insert-law").click();
  await page.locator("#modal-picker.open").waitFor();
}

// Searches the archive and clicks the result whose cite matches exactly
// (e.g. "21-210", not "21-2101"). Waits for the anchor's detail panel — the
// containers/blocks derived from it — to render before returning.
export async function chooseAnchor(page: Page, cite: string): Promise<void> {
  await page.locator("#picker-query").fill(cite);
  const result = page.locator(".picker-result", {
    has: page.locator(".picker-cite", { hasText: new RegExp(`^\\u00a7 ${cite}$`) }),
  });
  await result.first().click();
  await page.locator("#picker-detail").waitFor();
}

export async function setOperation(page: Page, operation: Operation): Promise<void> {
  await page.locator(OPERATION_RADIO[operation]).check();
}

// Picks the addition container by the key `additionTargets()` assigns it
// (corpus.js): "path" is the anchor's enclosing chapter/subchapter (new
// section), "section" is the anchor section itself (new subdivision).
export async function chooseContainer(page: Page, key: "path" | "section" | string): Promise<void> {
  await page.locator(`#picker-container-${key}`).check();
}

export async function fillAddition(
  page: Page,
  { designator, heading }: { designator: string; heading?: string }
): Promise<void> {
  await page.locator("#picker-new-designator").fill(designator);
  if (heading) {
    await page.locator("#picker-new-heading").fill(heading);
  }
}

// Selects a specific subunit to repeal by its rendered designator label, e.g.
// "a" for "a. Definitions. ...", and un-checks "the entire section" — checked
// by default (main.js renderPickerBlocks) — so only that subunit is repealed.
export async function chooseRepealBlock(page: Page, designator: string): Promise<void> {
  await page.locator("#picker-whole").uncheck();
  const label = page.locator("#picker-blocks label", {
    hasText: new RegExp(`^${designator}\\.\\s`),
  });
  await label.click();
}

export async function insertFromPicker(page: Page): Promise<void> {
  await page.locator("#btn-picker-insert").click();
  // closeModal() (main.js) removes the "open" class, which is what hides the
  // modal via CSS; the element itself stays in the DOM.
  await page.locator("#modal-picker").waitFor({ state: "hidden" });
}
