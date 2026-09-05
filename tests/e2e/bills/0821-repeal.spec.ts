import { test, expect, type Page } from "@playwright/test";
import { signIn, uniqueTestEmail } from "../support/auth";
import { createDraft, fetchDraftDoc, findBillSection, lawBlocks, textOf, waitForSave } from "../support/draft";
import { openPicker, chooseAnchor, setOperation, chooseContainer, fillAddition, chooseRepealBlock, insertFromPicker } from "../support/picker";
import { makePublic } from "../support/share";
import { downloadExport } from "../support/exports";

// Reproduces Int. 0821-2026 (../nyc_legislation/introduction/2026/0821.json)
// end to end: an "add" (a new subdivision g of 21-201), a repeal (subdivision
// a of 21-210, 21-216 and 21-217), another "add" (a wholly new section
// 21-218), and the effective date. The one structural gap the picker cannot
// close: the real bill's §2 repeals all three sections in a single sentence
// ("... are REPEALED"), because the picker resolves one anchor section at a
// time (EDITOR_PLAN.md §5) — so here that becomes three separate repeal bill
// sections instead, each by hand given the "The definition of ... in" prefix
// the real bill's combined sentence carries (see the loop below). Everything
// else — the title, every provision's wording, the effective date — is
// asserted against the bill's own Text field.

const SUBDIVISION_G_TEXT =
  '"Older adult center" shall mean a facility, other than a social adult day care, operated by a person pursuant to a contract with the department to provide services to older adults on a regular basis including, but not limited to, meals, recreation, and counseling.';

const REPEALED_SECTIONS = ["21-210", "21-216", "21-217"];

const SECTION_21_218 = {
  designator: "21-218",
  heading: "Non-digital access to forms and services.",
  subdivisions: [
    'Definitions. For purposes of this section, the following terms have the following meanings: Covered services. The term "covered services" means any information that is made available to the public regarding services offered or administered by the department, including, but not limited to, any application or enrollment information regarding such services. Digital. The term "digital" means access to information or materials through a website, online portal, or mobile web application. Non-digital. The term "non-digital" means access to information or materials through means other than digital technology, including printed material and telephonic technology.',
    "Non-digital access to covered services. The department shall ensure that covered services that are made available online or in a digital format are accessible at the same level of functionality through non-digital means, including by phone and in paper format. Access to covered services by phone shall include the ability to initiate and complete applications, receive guidance regarding additional documentation necessary to support an application, learn the status of an application, and request that covered services be sent to a recipient by mail, which shall be sent within 5 business days of such a request, unless the department has reason to believe that the safety of an older adult would be jeopardized by mailing such materials. Access to covered services shall also be made available at older adult centers in paper format.",
    "Accessibility. Non-digital access to covered services required pursuant to subdivision b of this section shall be provided in English and the designated citywide languages as defined in section 23-1101.",
  ],
};

const EFFECTIVE_DATE = "This local law takes effect 180 days after it becomes law.";

const TITLE =
  "requiring the department for the aging to maintain non-digital access to forms and services, and to repeal certain repetitions of the definition of “older adult center” in title 21 of such code";

test("amending, repealing and adding provisions of title 21 (Int. 0821-2026)", async ({ page, baseURL }) => {
  // "complimentary" reaches the Plus-gated features this test also exercises
  // (a public share link, export) without a real PayPal subscription — see
  // devPlan in editor_auth.go.
  const email = uniqueTestEmail("0821");
  await signIn(page, email, "/new", "complimentary");

  const id = await createDraft(page, { code: "administrative code", title: TITLE });

  // The draft is intentionally left behind (not deleted) so it can be opened
  // and read by hand after the run; sign in as the same test address again
  // (GET /_admin/testing/auth?email=...) to edit it, or use the public link
  // logged below to just read it.
  console.log(`[0821] signed in as ${email}`);
  console.log(`[0821] draft: ${baseURL}/d/${id}`);

  await draftBill(page, id);

  const publicURL = await makePublic(page);
  console.log(`[0821] public link: ${publicURL}`);
});

async function draftBill(page: Page, id: string) {
  // Section 1: "... is amended by adding a new subdivision g to read as
  // follows:" — an addition anchored on the section itself, not its chapter.
  await openPicker(page);
  await chooseAnchor(page, "21-201");
  await setOperation(page, "add");
  await chooseContainer(page, "section");
  await fillAddition(page, { designator: "g" });
  await insertFromPicker(page);
  await page.keyboard.type(SUBDIVISION_G_TEXT);

  // The title we set at creation already states the repeal generically, the
  // way the real bill's title does — don't also let the picker append its
  // own per-section clause for each of the three repeals below.
  //
  // composeLeadIn() (corpus.js) only ever names what is repealed by citation
  // ("Subdivision a of section 21-210 ... is REPEALED."); the real bill also
  // says *what* that provision is ("The definitions of 'older adult center'
  // in ... are REPEALED.") — description Rule 2.1.1 requires in the title but
  // does not require in the lead-in itself, so a drafter adds it by hand. The
  // lead-in is ordinary editable prose (EDITOR_PLAN.md §5), so this is typed
  // in like any other lead-in edit: the cursor goes to the very start (so the
  // auto-generated citation keeps its `ref` mark rather than being retyped
  // from scratch) and only the prefix is inserted, then "Subdivision" is
  // lowercased now that it no longer opens the sentence.
  for (const cite of REPEALED_SECTIONS) {
    await openPicker(page);
    await chooseAnchor(page, cite);
    await setOperation(page, "repeal");
    await chooseRepealBlock(page, "a");
    await page.locator("#picker-title-clause").uncheck();
    await insertFromPicker(page);

    // Click the very top-left of the paragraph, not click()'s default center —
    // this sentence is long enough to wrap onto a second visual line, and
    // "Home" only reaches the start of the *current* line, not the paragraph.
    const lead = page.locator(`.bill-section[data-kind="repeal"][data-cite="${cite}"] .section-lead`);
    await lead.click({ position: { x: 0, y: 0 } });
    await page.keyboard.press("Home");
    await page.keyboard.type('The definition of "older adult center" in ');
    await page.keyboard.press("Shift+ArrowRight");
    await page.keyboard.type("s");
  }

  // "Chapter 2 of title 21 ... is amended by adding a new section 21-218 to
  // read as follows:" — a second addition, anchored on a sibling section.
  await openPicker(page);
  await chooseAnchor(page, "21-217");
  await setOperation(page, "add");
  await chooseContainer(page, "path");
  await fillAddition(page, { designator: SECTION_21_218.designator, heading: SECTION_21_218.heading });
  await insertFromPicker(page);
  for (const text of SECTION_21_218.subdivisions) {
    await page.keyboard.press("Enter");
    await page.keyboard.type(text);
  }

  const effectiveLead = page.locator('.bill-section[data-kind="effective"] .section-lead');
  await effectiveLead.click({ clickCount: 3 });
  await page.keyboard.type(EFFECTIVE_DATE);

  await waitForSave(page, id);
  const doc = await fetchDraftDoc(page, id);

  expect(doc.content[0].type).toBe("bill_title");
  expect(doc.content[0].attrs.subject).toBe(TITLE);

  const addedG = findBillSection(doc, { kind: "add", cite: "21-201" });
  expect(addedG, "bill_section adding subdivision g of 21-201").toBeTruthy();
  const gBlocks = lawBlocks(addedG);
  expect(gBlocks).toHaveLength(1);
  expect(gBlocks[0].attrs.level).toBe("subdivision");
  expect(gBlocks[0].attrs.designator).toBe("g");
  expect(textOf(gBlocks[0])).toBe(SUBDIVISION_G_TEXT);
  expect(textOf(addedG.content[0])).toBe(
    "Section 21-201 of the administrative code of the city of New York is amended by adding a new subdivision g to read as follows:"
  );

  const leads: string[] = [];
  for (const cite of REPEALED_SECTIONS) {
    const billSection = findBillSection(doc, { kind: "repeal", cite });
    expect(billSection, `repeal bill_section for ${cite}`).toBeTruthy();
    expect(lawBlocks(billSection)).toHaveLength(0);
    const lead = textOf(billSection.content[0]);
    expect(lead).toBe(
      `The definition of "older adult center" in subdivision a of section ${cite} of the administrative code of the city of New York is REPEALED.`
    );
    leads.push(lead);
  }

  const added218 = findBillSection(doc, { kind: "add", cite: SECTION_21_218.designator });
  expect(added218, "bill_section adding 21-218").toBeTruthy();
  const blocks218 = lawBlocks(added218);
  expect(blocks218).toHaveLength(SECTION_21_218.subdivisions.length + 1);
  expect(blocks218[0].attrs.designator).toBe(SECTION_21_218.designator);
  expect(textOf(blocks218[0])).toBe(SECTION_21_218.heading);
  ["a", "b", "c"].forEach((designator, i) => {
    expect(blocks218[i + 1].attrs.designator).toBe(designator);
    expect(textOf(blocks218[i + 1])).toBe(SECTION_21_218.subdivisions[i]);
  });

  const effectiveSection = findBillSection(doc, { kind: "effective" });
  expect(textOf(effectiveSection.content[0])).toBe(EFFECTIVE_DATE);

  const markdown = await downloadExport(page, "download-markdown");
  expect(markdown).toContain(`<u>${SUBDIVISION_G_TEXT}</u>`);
  for (const lead of leads) {
    expect(markdown).toContain(lead);
  }
  expect(markdown).toContain(`<u>${SECTION_21_218.heading}</u>`);
  expect(markdown).toContain(EFFECTIVE_DATE);
}
