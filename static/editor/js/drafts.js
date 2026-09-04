// Persistence for the editor.
//
// The document lives in Firestore and is reached by its id; access is decided
// by the session cookie. localStorage keeps a copy of the last state so a
// failed save is visible rather than silent data loss.

const LOCAL_PREFIX = "intro.nyc.editor.doc.";

export function documentID() {
  const el = document.querySelector("[data-document-id]");
  return el ? el.dataset.documentId : "";
}

export function canShare() {
  const el = document.querySelector("[data-can-share]");
  return Boolean(el && el.dataset.canShare === "true");
}

export function plan() {
  const el = document.querySelector("[data-plan]");
  return el ? el.dataset.plan : "free";
}

export function loadLocal(id) {
  const raw = localStorage.getItem(LOCAL_PREFIX + id);
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch (e) {
    console.warn("could not restore local copy", e);
    return null;
  }
}

export function saveLocal(id, docJSON) {
  try {
    localStorage.setItem(LOCAL_PREFIX + id, JSON.stringify(docJSON));
  } catch (e) {
    // A quota failure must not interrupt drafting.
    console.warn("could not keep a local copy", e);
  }
}

export function clearLocal(id) {
  localStorage.removeItem(LOCAL_PREFIX + id);
}

export async function fetchDocument(id) {
  const response = await fetch(`/api/draft/${encodeURIComponent(id)}`);
  if (!response.ok) throw new Error(`could not load document (${response.status})`);
  return response.json();
}

export async function saveDocument(id, { title, code, doc }) {
  const response = await fetch(`/api/draft/${encodeURIComponent(id)}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ title, code, doc }),
  });
  if (!response.ok) throw new Error(`save failed (${response.status})`);
  return response.json();
}

export async function saveSharing(id, { editors, viewers, isPublic }) {
  const response = await fetch(`/api/share/${encodeURIComponent(id)}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ editors, viewers, public: isPublic }),
  });
  if (!response.ok) {
    const text = (await response.text()).trim();
    throw new Error(text || `could not update sharing (${response.status})`);
  }
  return response.json();
}

export function documentURL(id) {
  return new URL(`/d/${id}`, location.origin).toString();
}
