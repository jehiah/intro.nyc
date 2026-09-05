// Cross-references know what they point at.
//
// A reference built under Rule 5 carries a `ref` mark naming the provision in
// the law archive (see schema.js). That is enough to answer the question a
// drafter actually has — "what does that section say?" — without leaving the
// draft: hovering a reference shows the law it cites, with a link to the
// publisher's text.

import { Plugin } from "prosemirror-state";

import { CODES, loadSection, publisherURL } from "./corpus.js";

const OPEN_DELAY = 250;
const CLOSE_DELAY = 200;

// Enough of the provision to recognise it; the link is there for the rest.
const EXCERPT = 600;

function escapeHTML(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

function refAttrs(span) {
  return {
    dataset: span.dataset.dataset || "",
    file: span.dataset.file || "",
    cite: span.dataset.cite || "",
    record: span.dataset.record || "",
  };
}

function linkHTML(attrs) {
  const url = publisherURL(attrs);
  if (!url) return "";
  return `<a class="law-ref-open" href="${escapeHTML(
    url
  )}" target="_blank" rel="noopener">Code Library <span aria-hidden="true">↗</span></a>`;
}

function headHTML(attrs, section) {
  const code = section ? (CODES[section.code] || {}).short || section.code : "";
  const heading = section ? section.heading : "";
  // The link is first so it floats level with the citation rather than below it.
  return `<div class="law-ref-head">
    ${linkHTML(attrs)}
    <span class="law-ref-cite">§ ${escapeHTML(attrs.cite)}</span>
    ${heading ? `<span class="law-ref-heading">${escapeHTML(heading)}</span>` : ""}
    ${code ? `<span class="law-ref-code">${escapeHTML(code)}</span>` : ""}
  </div>`;
}

// The text as the archive files it: one entry per division, labelled the way
// the law labels it. Tables and publisher apparatus are left out.
function bodyHTML(section) {
  let used = 0;
  const parts = [];
  for (const block of section.blocks || []) {
    if (block.type || !block.text) continue;
    if (used >= EXCERPT) {
      parts.push('<p class="law-ref-more">…</p>');
      break;
    }
    const text = block.text.slice(0, EXCERPT - used);
    used += text.length;
    const label = block.designator ? `${block.designator}. ` : "";
    parts.push(
      `<p><span class="law-ref-designator">${escapeHTML(
        label
      )}</span>${escapeHTML(text)}${
        text.length < block.text.length ? "…" : ""
      }</p>`
    );
  }
  return parts.join("");
}

export function refPopoverPlugin() {
  let card = null;
  let anchor = null;
  let openTimer = null;
  let closeTimer = null;

  function ensureCard() {
    if (card) return card;
    card = document.createElement("div");
    card.className = "law-ref-card";
    card.hidden = true;
    card.addEventListener("mouseenter", () => clearTimeout(closeTimer));
    card.addEventListener("mouseleave", scheduleClose);
    document.body.appendChild(card);
    return card;
  }

  function place(span) {
    const box = span.getBoundingClientRect();
    const width = Math.min(30 * 16, window.innerWidth - 24);
    card.style.width = width + "px";
    const left = Math.min(
      Math.max(12, box.left + window.scrollX),
      window.scrollX + window.innerWidth - width - 12
    );
    card.style.left = left + "px";

    // Below the reference, unless the law runs off the bottom of the window and
    // there is more room above it.
    const height = card.offsetHeight;
    const below = window.innerHeight - box.bottom;
    const above = box.top;
    card.style.top =
      height + 12 > below && above > below
        ? box.top + window.scrollY - height - 6 + "px"
        : box.bottom + window.scrollY + 6 + "px";
  }

  function open(span) {
    anchor = span;
    const attrs = refAttrs(span);
    ensureCard();
    card.innerHTML =
      headHTML(attrs, null) +
      '<p class="law-ref-loading">Loading the law…</p>';
    card.hidden = false;
    place(span);

    if (!attrs.dataset || !attrs.file) return;
    loadSection(attrs).then(
      (section) => {
        // The pointer may have moved on while the section was loading.
        if (anchor !== span) return;
        card.innerHTML = headHTML(attrs, section) + bodyHTML(section);
        place(span);
      },
      () => {
        if (anchor !== span) return;
        card.innerHTML =
          headHTML(attrs, null) +
          '<p class="law-ref-loading">Could not load this section.</p>';
      }
    );
  }

  function close() {
    anchor = null;
    if (card) card.hidden = true;
  }

  function scheduleClose() {
    clearTimeout(openTimer);
    clearTimeout(closeTimer);
    closeTimer = setTimeout(close, CLOSE_DELAY);
  }

  function onOver(event) {
    const el = event.target instanceof Element ? event.target : null;
    const span = el && el.closest(".law-ref");
    if (span) {
      clearTimeout(closeTimer);
      if (anchor === span) return;
      clearTimeout(openTimer);
      openTimer = setTimeout(() => open(span), OPEN_DELAY);
      return;
    }
    if (el && el.closest(".law-ref-card")) {
      clearTimeout(closeTimer);
      return;
    }
    clearTimeout(openTimer);
    if (anchor) scheduleClose();
  }

  function onKey(event) {
    if (event.key === "Escape") close();
  }

  // The card is positioned against the page, so it does not follow what it
  // points at once the page moves under it.
  const onScroll = () => close();

  return new Plugin({
    view() {
      document.addEventListener("mouseover", onOver);
      document.addEventListener("keydown", onKey);
      window.addEventListener("scroll", onScroll, true);
      return {
        destroy() {
          document.removeEventListener("mouseover", onOver);
          document.removeEventListener("keydown", onKey);
          window.removeEventListener("scroll", onScroll, true);
          clearTimeout(openTimer);
          clearTimeout(closeTimer);
          if (card) card.remove();
          card = null;
        },
      };
    },
  });
}
