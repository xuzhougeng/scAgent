let confirmModalResolver = null;

export function bindImageModal() {
  const modal = document.getElementById("imageModal");
  const closeButton = document.getElementById("imageModalClose");
  const backdrop = document.getElementById("imageModalBackdrop");

  closeButton.addEventListener("click", closeImageModal);
  backdrop.addEventListener("click", closeImageModal);

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      closeImageModal();
    }
  });

  modal.addEventListener("click", (event) => {
    if (event.target === modal) {
      closeImageModal();
    }
  });
}

export function openImageModal(url, title) {
  const modal = document.getElementById("imageModal");
  const titleNode = document.getElementById("imageModalTitle");
  const image = document.getElementById("imageModalImage");
  const openLink = document.getElementById("imageModalOpen");
  const downloadLink = document.getElementById("imageModalDownload");

  titleNode.textContent = title || "结果预览";
  image.src = url;
  image.alt = title || "结果预览";
  openLink.href = url;
  downloadLink.href = url;
  downloadLink.setAttribute("download", "");
  modal.classList.remove("hidden");
  modal.setAttribute("aria-hidden", "false");
}

export function closeImageModal() {
  const modal = document.getElementById("imageModal");
  const image = document.getElementById("imageModalImage");
  if (modal.classList.contains("hidden")) {
    return;
  }
  modal.classList.add("hidden");
  modal.setAttribute("aria-hidden", "true");
  image.removeAttribute("src");
}

export function bindStatusOverviewModal() {
  const modal = document.getElementById("statusOverviewModal");
  const closeButton = document.getElementById("statusOverviewModalClose");
  const backdrop = document.getElementById("statusOverviewModalBackdrop");

  closeButton.addEventListener("click", closeStatusOverviewModal);
  backdrop.addEventListener("click", closeStatusOverviewModal);

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      closeStatusOverviewModal();
    }
  });

  modal.addEventListener("click", (event) => {
    if (event.target === modal) {
      closeStatusOverviewModal();
    }
  });
}

export function openStatusOverviewModal() {
  const modal = document.getElementById("statusOverviewModal");
  modal.classList.remove("hidden");
  modal.setAttribute("aria-hidden", "false");
}

export function closeStatusOverviewModal() {
  const modal = document.getElementById("statusOverviewModal");
  if (modal.classList.contains("hidden")) {
    return;
  }
  modal.classList.add("hidden");
  modal.setAttribute("aria-hidden", "true");
}

export function bindWorkspaceFilesModal() {
  const modal = document.getElementById("workspaceFilesModal");
  const closeButton = document.getElementById("workspaceFilesModalClose");
  const backdrop = document.getElementById("workspaceFilesModalBackdrop");

  closeButton.addEventListener("click", closeWorkspaceFilesModal);
  backdrop.addEventListener("click", closeWorkspaceFilesModal);

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      closeWorkspaceFilesModal();
    }
  });

  modal.addEventListener("click", (event) => {
    if (event.target === modal) {
      closeWorkspaceFilesModal();
    }
  });
}

export function bindPlannerPreviewModal() {
  const modal = document.getElementById("plannerPreviewModal");
  const closeButton = document.getElementById("plannerPreviewModalClose");
  const backdrop = document.getElementById("plannerPreviewModalBackdrop");

  closeButton.addEventListener("click", closePlannerPreviewModal);
  backdrop.addEventListener("click", closePlannerPreviewModal);

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      closePlannerPreviewModal();
    }
  });

  modal.addEventListener("click", (event) => {
    if (event.target === modal) {
      closePlannerPreviewModal();
    }
  });
}

export function openWorkspaceFilesModal() {
  const modal = document.getElementById("workspaceFilesModal");
  modal.classList.remove("hidden");
  modal.setAttribute("aria-hidden", "false");
}

export function closeWorkspaceFilesModal() {
  const modal = document.getElementById("workspaceFilesModal");
  if (modal.classList.contains("hidden")) {
    return;
  }
  modal.classList.add("hidden");
  modal.setAttribute("aria-hidden", "true");
}

export function openPlannerPreviewModal() {
  const modal = document.getElementById("plannerPreviewModal");
  modal.classList.remove("hidden");
  modal.setAttribute("aria-hidden", "false");
}

export function closePlannerPreviewModal() {
  const modal = document.getElementById("plannerPreviewModal");
  if (modal.classList.contains("hidden")) {
    return;
  }
  modal.classList.add("hidden");
  modal.setAttribute("aria-hidden", "true");
}

export function bindConfirmModal() {
  const modal = document.getElementById("confirmModal");
  const backdrop = document.getElementById("confirmModalBackdrop");
  const cancelButton = document.getElementById("confirmModalCancel");
  const confirmButton = document.getElementById("confirmModalConfirm");

  cancelButton.addEventListener("click", () => {
    settleConfirmModal(false);
  });
  confirmButton.addEventListener("click", () => {
    settleConfirmModal(true);
  });
  backdrop.addEventListener("click", () => {
    settleConfirmModal(false);
  });

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      settleConfirmModal(false);
    }
  });

  modal.addEventListener("click", (event) => {
    if (event.target === modal) {
      settleConfirmModal(false);
    }
  });
}

export function openConfirmModal({
  eyebrow = "确认操作",
  title = "确认执行这个操作？",
  message = "",
  confirmLabel = "确认",
  cancelLabel = "取消",
  danger = false,
} = {}) {
  if (confirmModalResolver) {
    settleConfirmModal(false);
  }

  const modal = document.getElementById("confirmModal");
  const eyebrowNode = document.getElementById("confirmModalEyebrow");
  const titleNode = document.getElementById("confirmModalTitle");
  const messageNode = document.getElementById("confirmModalMessage");
  const cancelButton = document.getElementById("confirmModalCancel");
  const confirmButton = document.getElementById("confirmModalConfirm");

  eyebrowNode.textContent = eyebrow;
  titleNode.textContent = title;
  messageNode.textContent = message;
  cancelButton.textContent = cancelLabel;
  confirmButton.textContent = confirmLabel;
  confirmButton.classList.toggle("danger", Boolean(danger));

  modal.classList.remove("hidden");
  modal.setAttribute("aria-hidden", "false");

  window.requestAnimationFrame(() => {
    confirmButton.focus();
  });

  return new Promise((resolve) => {
    confirmModalResolver = resolve;
  });
}

export function closeConfirmModal() {
  settleConfirmModal(false);
}

function settleConfirmModal(result) {
  const modal = document.getElementById("confirmModal");
  if (!modal || modal.classList.contains("hidden")) {
    return;
  }

  modal.classList.add("hidden");
  modal.setAttribute("aria-hidden", "true");

  if (confirmModalResolver) {
    const resolve = confirmModalResolver;
    confirmModalResolver = null;
    resolve(result);
  }
}
