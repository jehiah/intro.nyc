// The drafts list: deleting a draft, behind a confirmation.

let pending = null;

function openModal(id) {
  document.getElementById(id).classList.add("open");
}
function closeModal(id) {
  document.getElementById(id).classList.remove("open");
}

function askToDelete(button) {
  pending = button.dataset.delete;
  document.getElementById("delete-title").textContent = button.dataset.title;
  openModal("modal-delete");
  document.getElementById("btn-delete-confirm").focus();
}

async function confirmDelete() {
  if (!pending) return;
  const button = document.getElementById("btn-delete-confirm");
  button.disabled = true;
  try {
    const response = await fetch(`/api/draft/${encodeURIComponent(pending)}`, {
      method: "DELETE",
    });
    if (!response.ok) throw new Error(`delete failed (${response.status})`);
    const row = document.querySelector(`[data-delete="${pending}"]`).closest("li");
    row.remove();
    closeModal("modal-delete");
    if (!document.querySelectorAll(".document-list li").length) {
      location.reload();
    }
  } catch (e) {
    console.error(e);
    document.getElementById("delete-title").textContent =
      "Could not delete that draft.";
  } finally {
    button.disabled = false;
    pending = null;
  }
}

document.querySelectorAll("[data-delete]").forEach((button) => {
  button.addEventListener("click", (e) => {
    e.preventDefault();
    askToDelete(button);
  });
});

document.getElementById("btn-delete-confirm").addEventListener("click", confirmDelete);

document.querySelectorAll("[data-close]").forEach((el) => {
  el.addEventListener("click", () => closeModal(el.dataset.close));
});

document.querySelectorAll(".editor-modal").forEach((modal) => {
  modal.addEventListener("mousedown", (e) => {
    if (e.target === modal) closeModal(modal.id);
  });
});

document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape") return;
  const open = document.querySelector(".editor-modal.open");
  if (open) closeModal(open.id);
});
