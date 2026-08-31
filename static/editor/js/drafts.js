// Persistence for the editor.
//
// Drafts are saved through the API; a draft's `secret` is the edit token and
// never leaves this browser. localStorage keeps the last document so a reload
// is instant and a save failure is not data loss.

const LOCAL_KEY = "intro.nyc.editor.draft";
const HANDLE_KEY = "intro.nyc.editor.handle";

export function loadLocal() {
  const raw = localStorage.getItem(LOCAL_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch (e) {
    console.warn("could not restore local draft", e);
    return null;
  }
}

export function saveLocal(docJSON) {
  localStorage.setItem(LOCAL_KEY, JSON.stringify(docJSON));
}

export function clearLocal() {
  localStorage.removeItem(LOCAL_KEY);
  localStorage.removeItem(HANDLE_KEY);
}

// { id, secret } for the draft this browser is allowed to edit.
export function loadHandle() {
  const raw = localStorage.getItem(HANDLE_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch (e) {
    return null;
  }
}

function saveHandle(handle) {
  localStorage.setItem(HANDLE_KEY, JSON.stringify(handle));
}

export async function saveDraft(handle, title, docJSON) {
  const response = await fetch("api/draft", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      id: handle ? handle.id : "",
      secret: handle ? handle.secret : "",
      title,
      doc: docJSON,
    }),
  });
  if (!response.ok) throw new Error(`save failed (${response.status})`);
  const saved = await response.json();
  const next = { id: saved.id, secret: saved.secret };
  saveHandle(next);
  return next;
}

export async function fetchDraft(id) {
  const response = await fetch(`api/draft/${encodeURIComponent(id)}`);
  if (!response.ok) throw new Error(`could not load draft ${id}`);
  return response.json();
}

export function readOnlyURL(id) {
  return new URL(`d/${id}`, document.baseURI).toString();
}
