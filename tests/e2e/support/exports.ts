import type { Page } from "@playwright/test";
import { readFileSync } from "node:fs";

// Drives the shared download menu (`download-menu` in templates/editor_base.html,
// wired in exports.js) and returns the text of a `data-export="download-*"`
// item's real browser download (an <a download> click, per exports.js).
export async function downloadExport(page: Page, exportKind: string): Promise<string> {
  await page.locator("#btn-download").click();
  await page.locator("#download-menu.open").waitFor();

  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.locator(`[data-export="${exportKind}"]`).click(),
  ]);
  const path = await download.path();
  if (!path) throw new Error(`download for ${exportKind} produced no file`);
  return readFileSync(path, "utf8");
}
