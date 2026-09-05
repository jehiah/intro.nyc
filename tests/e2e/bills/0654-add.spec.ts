import { test, expect, type Page } from "@playwright/test";
import { signIn, uniqueTestEmail } from "../support/auth";
import { createDraft, fetchDraftDoc, findBillSection, lawBlocks, textOf, allTextMarked, waitForSave } from "../support/draft";
import { openPicker, chooseAnchor, setOperation, chooseContainer, fillAddition, insertFromPicker } from "../support/picker";
import { makePublic } from "../support/share";
import { downloadExport } from "../support/exports";

// Reproduces, through the same UI sequence a drafter would use, the shape of
// Int. 0654-2026 (../nyc_legislation/introduction/2026/0654.json): a single
// "add" bill section carrying two wholly new sections, 19-175.8 and 19-175.9,
// each with several subdivisions, followed by an effective date. This
// exercises the tracked "add" engine (EDITOR_PLAN.md §4-5): every typed
// character should carry the `ins` mark, `Enter` should split into the next
// provision at the right level, and designators should auto-sequence a, b, c...

const SECTION_19_175_8 = {
  designator: "19-175.8",
  heading: "Hazardous obstruction.",
  subdivisions: [
    "Except as otherwise permitted by law, no person shall park, stop or stand a vehicle within a radial distance of 2640 feet of a school building, entrance or exit in a manner that obstructs a bicycle lane, bus lane when bus lane restrictions are in effect, sidewalk, crosswalk or fire hydrant.",
    "As an alternative to any other means of enforcement authorized by law, a violation of subdivision a of this section shall be punishable by a civil penalty of $175. Such civil penalties shall be recoverable in a proceeding before the office of administrative trials and hearings.",
  ],
};

const SECTION_19_175_9 = {
  designator: "19-175.9",
  heading: "Civilian complaint of hazardous obstruction.",
  subdivisions: [
    "Any natural person, excluding personnel of the department and other employees of the city authorized to serve summonses for violations of section 19-175.8, may serve upon the department a complaint, in a form prescribed by the commissioner, alleging that a person has violated section 19-175.8.",
    "The department shall publish on its website information on filing civilian complaints pursuant to this section. Such information shall include but need not be limited to instructions for filing such complaints and for gathering supporting documentation.",
    "The department shall provide a tracking number to each person who submits a civilian complaint pursuant to subdivision a of this section which shall allow such person to track the status of such complaint from initiation to disposition. The department shall provide an initial status update for any such civilian complaint within three days of the submission of such complaint.",
    "In any proceeding brought by the department based on a complaint submitted pursuant to subdivision a of this section, the office of administrative trials and hearings shall award the complainant 25 percent of any sums collected as a result of such proceeding.",
    "No later than one year after the effective date of the local law that added this section, and annually thereafter, the commissioner shall submit to the speaker of the council and post on the department's website a report including the number of complaints submitted pursuant to subdivision a of this section and the dispositions of such complaints.",
    "The commissioner shall promulgate such rules as are necessary to implement the provisions of this section.",
  ],
};

const EFFECTIVE_DATE = "This local law takes effect 120 days after becoming law.";

test("drafting new sections 19-175.8 and 19-175.9 (Int. 0654-2026)", async ({ page, baseURL }) => {
  // "complimentary" reaches the Plus-gated features this test also exercises
  // (a public share link, export) without a real PayPal subscription — see
  // devPlan in editor_auth.go.
  const email = uniqueTestEmail("0654");
  await signIn(page, email, "/new", "complimentary");

  const id = await createDraft(page, {
    code: "administrative code",
    title:
      "hazardous obstruction by vehicles and civilian complaints to the department of transportation for hazardous obstruction violations",
  });

  // The draft is intentionally left behind (not deleted) so it can be opened
  // and read by hand after the run; sign in as the same test address again
  // (GET /_admin/testing/auth?email=...) to edit it, or use the public link
  // logged below to just read it.
  console.log(`[0654] signed in as ${email}`);
  console.log(`[0654] draft: ${baseURL}/d/${id}`);

  await draftBill(page, id);

  const publicURL = await makePublic(page);
  console.log(`[0654] public link: ${publicURL}`);
});

async function draftBill(page: Page, id: string) {
  for (const section of [SECTION_19_175_8, SECTION_19_175_9]) {
    await openPicker(page);
    await chooseAnchor(page, "19-175");
    await setOperation(page, "add");
    // additionTargets() offers the anchor's enclosing chapter/subchapter as a
    // "new section" container — the key `describePath` files it under.
    await chooseContainer(page, "path");
    await fillAddition(page, { designator: section.designator, heading: section.heading });
    await insertFromPicker(page);

    // Cursor lands at the end of the heading text (insertBillSection). Enter
    // starts the next provision; splitting a section-level block starts a
    // subdivision (track.js), so designators auto-sequence a, b, c...
    for (const text of section.subdivisions) {
      await page.keyboard.press("Enter");
      await page.keyboard.type(text);
    }
  }

  // The effective-date sentence is free text (EDITOR_PLAN.md §"Effective date
  // section"), edited by selecting the line and retyping it.
  const effectiveLead = page.locator('.bill-section[data-kind="effective"] .section-lead');
  await effectiveLead.click({ clickCount: 3 }); // selects the whole sentence
  await page.keyboard.type(EFFECTIVE_DATE);

  await waitForSave(page, id);
  const doc = await fetchDraftDoc(page, id);

  expect(doc.content[0].type).toBe("bill_title");
  expect(doc.content[0].attrs.code).toBe("administrative code");

  for (const section of [SECTION_19_175_8, SECTION_19_175_9]) {
    const billSection = findBillSection(doc, { kind: "add", cite: section.designator });
    expect(billSection, `bill_section for ${section.designator}`).toBeTruthy();

    const blocks = lawBlocks(billSection);
    expect(blocks).toHaveLength(section.subdivisions.length + 1);
    // Rule 11.1: none of this text was ever law, so all of it is underlined —
    // every law_block (not the unmarked section_lead) carries `ins` throughout.
    expect(blocks.every((b: any) => allTextMarked(b, "ins"))).toBe(true);

    expect(blocks[0].attrs.level).toBe("section");
    expect(blocks[0].attrs.designator).toBe(section.designator);
    expect(textOf(blocks[0])).toBe(section.heading);

    const expectedDesignators = "abcdefghij".slice(0, section.subdivisions.length).split("");
    section.subdivisions.forEach((text, i) => {
      expect(blocks[i + 1].attrs.level).toBe("subdivision");
      expect(blocks[i + 1].attrs.designator).toBe(expectedDesignators[i]);
      expect(textOf(blocks[i + 1])).toBe(text);
    });
  }

  const effectiveSection = findBillSection(doc, { kind: "effective" });
  expect(textOf(effectiveSection.content[0])).toBe(EFFECTIVE_DATE);

  // toMarkdown() (serialize.js) has no underline, so every added run is
  // wrapped in <u> instead — verify the download reflects the same tracked
  // marks the doc JSON already confirmed, not just that a file came down.
  const markdown = await downloadExport(page, "download-markdown");
  expect(markdown).toContain(`<u>${SECTION_19_175_8.heading}</u>`);
  expect(markdown).toContain(`<u>${SECTION_19_175_8.subdivisions[0]}</u>`);
  expect(markdown).toContain(`<u>${SECTION_19_175_9.subdivisions[5]}</u>`);
  expect(markdown).toContain(EFFECTIVE_DATE);
}
