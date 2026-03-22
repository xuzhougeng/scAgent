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
