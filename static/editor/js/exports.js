// The download menu and the actions behind it, shared by the editor and the
// read-only bill so a shared draft exports exactly as the drafter's copy does.
//
// Nothing here decides whether an export is allowed. Items the reader may not
// use are rendered disabled by the server, and a disabled button emits no
// click, so the gate is visible in the markup rather than enforced in JS.

function download(filename, text, type = "text/plain") {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

// Each action takes the document and the serializers, and returns what to say
// about it — a download speaks for itself, a copy does not.
const EXPORTS = {
  "copy-rich": async (doc, s) => {
    const text = s.toPlainText(doc);
    try {
      await navigator.clipboard.write([
        new ClipboardItem({
          "text/html": new Blob([s.toRichText(doc)], { type: "text/html" }),
          "text/plain": new Blob([text], { type: "text/plain" }),
        }),
      ]);
      return "Copied — paste into the Legislative Division template";
    } catch (e) {
      await navigator.clipboard.writeText(text);
      return "Copied as plain text";
    }
  },
  "copy-text": async (doc, s) => {
    await navigator.clipboard.writeText(s.toPlainText(doc));
    return "Copied bill text";
  },
  "copy-markdown": async (doc, s) => {
    await navigator.clipboard.writeText(s.toMarkdown(doc));
    return "Copied markdown";
  },
  "download-text": (doc, s) => download("bill.txt", s.toPlainText(doc)),
  "download-markdown": (doc, s) =>
    download("bill.md", s.toMarkdown(doc), "text/markdown"),
  "download-html": (doc, s) =>
    download("bill.html", s.toHTMLDocument(doc), "text/html"),
  "download-adopted": (doc, s) =>
    download("bill-as-adopted.txt", s.toAdoptedText(doc)),
  "download-json": (doc) =>
    download(
      "bill.json",
      JSON.stringify(doc.toJSON(), null, 2),
      "application/json"
    ),
};

// wireDownloadMenu connects the download button to the menu and the menu to the
// exports. `getDoc` may be async, so the read-only view can leave the document
// on the server until someone asks for it.
export function wireDownloadMenu(getDoc, onStatus = () => {}) {
  const menu = document.getElementById("download-menu");
  const button = document.getElementById("btn-download");
  if (!menu || !button) return;

  button.addEventListener("click", (e) => {
    e.stopPropagation();
    const open = menu.classList.toggle("open");
    button.setAttribute("aria-expanded", String(open));
  });
  document.addEventListener("click", () => {
    menu.classList.remove("open");
    button.setAttribute("aria-expanded", "false");
  });

  menu.addEventListener("click", async (e) => {
    // The lock icon inside a menu item is a click target of its own.
    const item = e.target.closest("[data-export]");
    if (!item || item.disabled) return;
    menu.classList.remove("open");
    // The serializers, and the document model they walk, are only needed once
    // an export has actually been chosen.
    const [serialize, doc] = await Promise.all([
      import("./serialize.js"),
      getDoc(),
    ]);
    onStatus((await EXPORTS[item.dataset.export](doc, serialize)) || "");
  });
}
