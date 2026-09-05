import type { Page } from "@playwright/test";

// A free account is capped at a handful of drafts (requireDraftCapacity,
// editor_billing.go); reusing one email across runs would eventually hit that
// cap. A fresh, random address gets a fresh UID (devTestUser, editor_auth.go)
// and therefore a fresh quota every run.
export function uniqueTestEmail(label: string): string {
  return `playwright-${label}-${Date.now()}-${Math.random().toString(36).slice(2)}@dev.intro.nyc`;
}

export type DevPlan = "complimentary" | "plus" | "free";

// Signs in through the dev-mode-only test route (editor_auth.go,
// EditorTestingAuth) instead of the real Firebase flow, and lands on
// `redirect` once the session cookie is set. `plan`, when given, overrides
// the normal (Firestore-backed) plan lookup for this session — pass
// "complimentary" to reach Plus-gated features (export, a public share link)
// without a real PayPal subscription.
export async function signIn(
  page: Page,
  email: string,
  redirect = "/",
  plan?: DevPlan
): Promise<void> {
  const params = new URLSearchParams({ email, redirect });
  if (plan) params.set("plan", plan);
  const response = await page.goto(`/_admin/testing/auth?${params}`);
  if (!response || !response.ok()) {
    throw new Error(
      `sign-in failed (${response?.status()}). Is the server running with -dev-mode?`
    );
  }
}
