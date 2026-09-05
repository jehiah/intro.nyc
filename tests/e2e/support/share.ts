import type { Page } from "@playwright/test";

// Drives `#modal-share` (templates/editor.html, wired in main.js): opens the
// share dialog, turns on the public link (requires Plus — see
// requireShareCapacity, editor_billing.go — so the signing-in test must use
// `plan: "complimentary"`), and returns the link `#share-url` shows for it.
export async function makePublic(page: Page): Promise<string> {
  await page.locator("#btn-share").click();
  await page.locator("#modal-share.open").waitFor();

  // commitSharing() (main.js) POSTs here on the checkbox's "change" event.
  const shared = page.waitForResponse(
    (r) => /\/api\/share\//.test(r.url()) && r.request().method() === "POST"
  );
  await page.locator("#share-public").check();
  const response = await shared;
  if (!response.ok()) {
    throw new Error(`making the draft public failed: ${response.status()} ${await response.text()}`);
  }
  const body = await response.json();
  if (!body.public) {
    throw new Error("server did not report the draft as public");
  }

  const url = await page.locator("#share-url").inputValue();
  await page.keyboard.press("Escape");
  await page.locator("#modal-share").waitFor({ state: "hidden" });
  return url;
}
