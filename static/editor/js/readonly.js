// The read-only bill.
//
// The bill itself is rendered server-side (renderBill in editor.go), so the
// page reads with JavaScript off. This is only the download menu: it fetches
// the stored document — the same one the editor loads — and hands it to the
// shared exporters, so a shared bill exports byte-for-byte as the drafter's
// copy does.

import { documentID, fetchDocument } from "./drafts.js";
import { wireDownloadMenu } from "./exports.js";

let doc = null;

async function billDoc() {
  if (!doc) {
    const [{ schema }, stored] = await Promise.all([
      import("./schema.js"),
      fetchDocument(documentID()),
    ]);
    doc = schema.nodeFromJSON(stored.doc);
  }
  return doc;
}

wireDownloadMenu(billDoc);
