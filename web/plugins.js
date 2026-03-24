import { t, initI18n, translateDOM } from "/js/i18n.mjs";
import { escapeHTML, escapeAttribute, formatSkillName, statusPill } from "/js/format.mjs";
import { fetchJSON } from "/js/api.mjs";

const hubState = {
  bundles: [],
  skills: [],
  status: "",
  busyBundles: new Set(),
  openBundles: new Set(),
  selectedBundleID: "",
  selectedSkillName: "",
  detailModalOpen: false,
};

await initI18n();
translateDOM();
bindHub();
await refreshHub();

function bindHub() {
  const uploadForm = document.getElementById("pluginUploadForm");
  const fileInput = document.getElementById("pluginFileInput");
  const refreshButton = document.getElementById("refreshButton");
  const bundleList = document.getElementById("bundleList");
  const detailModal = document.getElementById("skillDetailModal");
  const detailCloseButton = document.getElementById("skillDetailClose");

  uploadForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const file = fileInput.files?.[0];
    if (!file) {
      setStatus(t("hub.selectZipFirst"), true);
      return;
    }

    const formData = new FormData();
    formData.append("file", file);
    setStatus(t("hub.installing", { name: file.name }));
    try {
      const response = await fetchJSON("/api/plugins", {
        method: "POST",
        body: formData,
      });
      hubState.bundles = response.bundles || response.plugins || [];
      hubState.skills = response.skills || [];
      fileInput.value = "";
      reconcileSelection();
      setStatus(t("hub.installed", { name: response.plugin?.name || file.name }));
      render();
    } catch (error) {
      setStatus(error.message, true);
    }
  });

  refreshButton.addEventListener("click", async () => {
    setStatus(t("hub.refreshing"));
    await refreshHub();
  });

  bundleList.addEventListener("click", async (event) => {
    const skillButton = event.target.closest("[data-select-skill]");
    if (skillButton) {
      hubState.selectedBundleID = skillButton.getAttribute("data-bundle-id") || "";
      hubState.selectedSkillName = skillButton.getAttribute("data-select-skill") || "";
      hubState.detailModalOpen = true;
      render();
      focusDetailModal();
      return;
    }

    const actionButton = event.target.closest("[data-bundle-id][data-next-enabled]");
    if (!actionButton) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();

    const bundleID = actionButton.getAttribute("data-bundle-id");
    const enabled = actionButton.getAttribute("data-next-enabled") === "true";
    if (!bundleID || hubState.busyBundles.has(bundleID)) {
      return;
    }

    hubState.busyBundles.add(bundleID);
    render();
    setStatus(enabled ? t("hub.enabling", { id: bundleID }) : t("hub.disabling", { id: bundleID }));
    try {
      const response = await fetchJSON(`/api/plugins/${encodeURIComponent(bundleID)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled }),
      });
      hubState.bundles = response.bundles || [];
      hubState.skills = response.skills || [];
      reconcileSelection();
      setStatus(enabled
        ? t("hub.enabled", { name: response.bundle?.name || bundleID })
        : t("hub.disabled", { name: response.bundle?.name || bundleID }));
    } catch (error) {
      setStatus(error.message, true);
    } finally {
      hubState.busyBundles.delete(bundleID);
      render();
    }
  });

  detailCloseButton?.addEventListener("click", () => {
    closeDetailModal();
  });

  detailModal?.addEventListener("click", (event) => {
    if (event.target instanceof HTMLElement && event.target.hasAttribute("data-close-skill-detail")) {
      closeDetailModal();
    }
  });

  document.addEventListener("keydown", (event) => {
    if (!hubState.detailModalOpen) {
      return;
    }
    if (event.key === "Escape") {
      event.preventDefault();
      closeDetailModal();
      return;
    }
    if (shouldIgnoreDetailKeydown(event.target) || event.altKey || event.ctrlKey || event.metaKey) {
      return;
    }

    if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
      if (navigateDetailSelection(-1)) {
        event.preventDefault();
      }
      return;
    }

    if (event.key === "ArrowRight" || event.key === "ArrowDown") {
      if (navigateDetailSelection(1)) {
        event.preventDefault();
      }
    }
  });
}

async function refreshHub() {
  try {
    const [pluginsResponse, skillsResponse] = await Promise.all([
      fetchJSON("/api/plugins"),
      fetchJSON("/api/skills"),
    ]);
    hubState.bundles = pluginsResponse.bundles || pluginsResponse.plugins || [];
    hubState.skills = skillsResponse.skills || [];
    reconcileSelection();
    setStatus(t("hub.refreshed"));
  } catch (error) {
    setStatus(error.message, true);
  }
  render();
}

function reconcileSelection() {
  const record = findSelectedSkillRecord();
  if (record) {
    hubState.openBundles.add(record.bundle.id);
    return;
  }

  const firstBundle = hubState.bundles.find((bundle) => (bundle.skills || []).length > 0);
  if (!firstBundle) {
    hubState.selectedBundleID = "";
    hubState.selectedSkillName = "";
    return;
  }

  const firstSkill = firstBundle.skills?.[0];
  hubState.selectedBundleID = firstBundle.id;
  hubState.selectedSkillName = firstSkill?.name || "";
  hubState.openBundles.add(firstBundle.id);
}

function render() {
  renderSummary();
  renderBundles();
  renderSkillDetail();
  syncDetailModal();
  bindBundleToggles();
}

function renderSummary() {
  const container = document.getElementById("hubSummary");
  const badge = document.getElementById("summaryBadge");
  const bundles = hubState.bundles || [];
  const enabledBundles = bundles.filter((bundle) => bundle.enabled);
  const enabledSkills = bundles
    .filter((bundle) => bundle.enabled)
    .reduce((count, bundle) => count + (bundle.skills || []).length, 0);

  if (badge) {
    badge.textContent = t("hub.enabledBadge", { enabled: enabledBundles.length, total: bundles.length || 0 });
  }

  container.innerHTML = `
    ${summaryCard(t("hub.totalBundles"), String(bundles.length))}
    ${summaryCard(t("hub.enabledBundles"), String(enabledBundles.length))}
    ${summaryCard(t("hub.externalPlugins"), String(bundles.filter((bundle) => !bundle.builtin).length))}
    ${summaryCard(t("hub.enabledSkillCount"), String(enabledSkills))}
  `;
}

function renderBundles() {
  const container = document.getElementById("bundleList");
  if (!hubState.bundles.length) {
    container.innerHTML = `<section class='empty-state'>${escapeHTML(t("hub.noBundles"))}</section>`;
    return;
  }

  container.innerHTML = hubState.bundles
    .map((bundle) => {
      const isBusy = hubState.busyBundles.has(bundle.id);
      const bundleType = bundle.builtin ? t("hub.builtinBundle") : t("hub.externalBundle");
      const isOpen = hubState.openBundles.has(bundle.id);
      return `
        <details class="bundle-card ${bundle.enabled ? "enabled" : "disabled"}" data-bundle-card="${escapeAttribute(bundle.id)}"${isOpen ? " open" : ""}>
          <summary class="bundle-summary">
            <div class="bundle-summary-main">
              <div class="bundle-title-row">
                <h3>${escapeHTML(bundle.name || bundle.id)}</h3>
                <span class="bundle-version">${escapeHTML(bundle.version || bundle.id)}</span>
              </div>
              <div class="pill-row">
                ${statusPill(bundle.enabled ? "ok" : "muted", bundle.enabled ? t("hub.statusEnabled") : t("hub.statusDisabled"))}
                ${statusPill(bundle.builtin ? "warn" : "muted", bundleType)}
                ${statusPill("muted", t("hub.skillCount", { count: (bundle.skills || []).length }))}
              </div>
            </div>
            <div class="bundle-summary-right">
              <span class="bundle-expand-hint">${t("hub.expandSkills")}</span>
              <span class="bundle-chevron" aria-hidden="true"></span>
            </div>
          </summary>

          <div class="bundle-body">
            <div class="bundle-actions">
              <button
                type="button"
                class="ghost-button"
                data-bundle-id="${escapeAttribute(bundle.id)}"
                data-next-enabled="${bundle.enabled ? "false" : "true"}"
                ${isBusy ? "disabled" : ""}
              >
                ${isBusy ? t("hub.processing") : (bundle.enabled ? (bundle.builtin ? t("hub.disableBundle") : t("hub.disablePlugin")) : (bundle.builtin ? t("hub.enableBundle") : t("hub.enablePlugin")))}
              </button>
            </div>

            ${
              bundle.description
                ? `<p class="bundle-description">${escapeHTML(bundle.description)}</p>`
                : ""
            }
            ${
              bundle.builtin
                ? `<p class="bundle-note">${escapeHTML(t("hub.builtinNote"))}</p>`
                : ""
            }
            <div class="bundle-meta">
              <span>${t("hub.source", { path: escapeHTML(bundle.source_path || "Skill Hub") })}</span>
            </div>

            <div class="bundle-skills-head">
              <strong>${t("hub.skillList")}</strong>
              <span class="muted">${t("hub.skillListHint")}</span>
            </div>
            <div class="skill-button-grid ${bundle.enabled ? "" : "disabled-skills"}">
              ${(bundle.skills || [])
                .map(
                  (skill) => `
                    <button
                      type="button"
                      class="skill-button ${isSelectedSkill(bundle.id, skill.name) ? "selected" : ""}"
                      data-bundle-id="${escapeAttribute(bundle.id)}"
                      data-select-skill="${escapeAttribute(skill.name)}"
                    >
                      <span class="skill-button-name">${escapeHTML(formatSkillName(skill.name))}</span>
                      <span class="skill-button-id">${escapeHTML(skill.name)}</span>
                    </button>
                  `,
                )
                .join("")}
            </div>
          </div>
        </details>
      `;
    })
    .join("");
}

function bindBundleToggles() {
  const detailsList = document.querySelectorAll("[data-bundle-card]");
  for (const details of detailsList) {
    details.addEventListener("toggle", () => {
      const bundleID = details.getAttribute("data-bundle-card");
      if (!bundleID) {
        return;
      }
      if (details.open) {
        hubState.openBundles.add(bundleID);
      } else {
        hubState.openBundles.delete(bundleID);
      }
    });
  }
}

function renderSkillDetail() {
  const container = document.getElementById("skillDetail");
  const record = findSelectedSkillRecord();
  if (!record) {
    container.innerHTML = `
      <section class="detail-empty">
        <strong>${t("hub.noSelection")}</strong>
        <p>${t("hub.noSelectionHint")}</p>
      </section>
    `;
    return;
  }

  const { bundle, skill } = record;
  const runtimeSpec = renderRuntimeSpec(bundle, skill);
  const inputEntries = Object.entries(skill.input || {});
  const outputEntries = Object.entries(skill.output || {});
  const targetKinds = Array.isArray(skill.target_kinds) ? skill.target_kinds : [];

  container.innerHTML = `
    <article class="detail-card">
      <div class="detail-head">
        <div>
          <div class="pill-row">
            ${statusPill(bundle.enabled ? "ok" : "muted", bundle.enabled ? t("hub.bundleEnabled") : t("hub.bundleDisabled"))}
            ${statusPill(bundle.builtin ? "warn" : "muted", bundle.builtin ? t("hub.builtinSkill") : t("hub.pluginSkill"))}
            ${statusPill(skill.support_level === "wired" || !skill.support_level ? "ok" : "muted", skill.support_level || "wired")}
          </div>
          <h4>${escapeHTML(formatSkillName(skill.name))}</h4>
          <p class="detail-subtitle">${escapeHTML(skill.name)}</p>
        </div>
      </div>

      <section class="detail-section">
        <h5>${t("hub.skillDescription")}</h5>
        <p>${escapeHTML(skill.description || t("hub.noDescription"))}</p>
      </section>

      <section class="detail-section detail-meta-grid">
        <div class="meta-card">
          <span class="meta-label">${t("hub.ownerBundle")}</span>
          <strong>${escapeHTML(bundle.name || bundle.id)}</strong>
          <p>${escapeHTML(bundle.id)}</p>
        </div>
        <div class="meta-card">
          <span class="meta-label">${t("hub.targetObjects")}</span>
          <strong>${targetKinds.length ? escapeHTML(targetKinds.join(", ")) : t("hub.noRestriction")}</strong>
          <p>${t("hub.targetObjectsHint")}</p>
        </div>
      </section>

      <section class="detail-section">
        <div class="detail-section-head">
          <h5>${t("hub.inputSpec")}</h5>
          <span class="muted">${t("ui.itemCount", { count: inputEntries.length })}</span>
        </div>
        ${renderInputTable(inputEntries)}
      </section>

      <section class="detail-section">
        <div class="detail-section-head">
          <h5>${t("hub.outputSpec")}</h5>
          <span class="muted">${t("ui.itemCount", { count: outputEntries.length })}</span>
        </div>
        ${renderOutputList(outputEntries)}
      </section>

      <section class="detail-section">
        <div class="detail-section-head">
          <h5>${t("hub.runtimeSpec")}</h5>
          <span class="muted">${bundle.builtin ? t("hub.builtinImpl") : t("hub.pluginEntry")}</span>
        </div>
        ${runtimeSpec}
      </section>
    </article>
  `;
}

function renderInputTable(entries) {
  if (!entries.length) {
    return `<p class='muted'>${escapeHTML(t("hub.noInputParams"))}</p>`;
  }
  return `
    <div class="spec-table">
      <div class="spec-row spec-head">
        <span>${t("hub.fieldName")}</span>
        <span>${t("hub.fieldType")}</span>
        <span>${t("hub.required")}</span>
        <span>${t("hub.fieldDescription")}</span>
      </div>
      ${entries
        .map(([name, schema]) => {
          const enumText = Array.isArray(schema.enum) && schema.enum.length
            ? t("hub.enumValues", { values: schema.enum.join(", ") })
            : "";
          const description = [schema.description || t("hub.noDescriptionShort"), enumText].filter(Boolean).join(" ");
          return `
            <div class="spec-row">
              <span class="spec-key">${escapeHTML(name)}</span>
              <span>${escapeHTML(schema.type || "any")}</span>
              <span>${schema.required ? t("hub.yesRequired") : t("hub.noOptional")}</span>
              <span>${escapeHTML(description)}</span>
            </div>
          `;
        })
        .join("")}
    </div>
  `;
}

function renderOutputList(entries) {
  if (!entries.length) {
    return `<p class='muted'>${escapeHTML(t("hub.noOutputFields"))}</p>`;
  }
  return `
    <div class="output-list">
      ${entries
        .map(
          ([name, value]) => `
            <div class="output-item">
              <strong>${escapeHTML(name)}</strong>
              <span>${escapeHTML(String(value || ""))}</span>
            </div>
          `,
        )
        .join("")}
    </div>
  `;
}

function renderRuntimeSpec(bundle, skill) {
  if (skill.runtime && Object.keys(skill.runtime).length > 0) {
    return `
      <div class="runtime-note">
        <p>${t("hub.runtimeNote")}</p>
      </div>
      <pre class="spec-code"><code>${escapeHTML(JSON.stringify(skill.runtime, null, 2))}</code></pre>
    `;
  }

  if (bundle.builtin) {
    return `
      <div class="runtime-note">
        <p>${t("hub.builtinRuntimeNote")}</p>
        <p>${t("hub.builtinRuntimeHint")}</p>
      </div>
    `;
  }

  return `<p class='muted'>${escapeHTML(t("hub.noRuntimeConfig"))}</p>`;
}

function syncDetailModal() {
  const modal = document.getElementById("skillDetailModal");
  const dialog = modal?.querySelector(".skill-detail-dialog");
  const hasSelection = Boolean(findSelectedSkillRecord());
  if (!modal || !dialog) {
    return;
  }
  if (!hasSelection) {
    hubState.detailModalOpen = false;
  }
  const isOpen = hubState.detailModalOpen;

  modal.hidden = !isOpen;
  modal.setAttribute("aria-hidden", isOpen ? "false" : "true");
  document.body.classList.toggle("detail-modal-open", isOpen);
}

function focusDetailModal() {
  window.requestAnimationFrame(() => {
    document.getElementById("skillDetailClose")?.focus();
  });
}

function closeDetailModal() {
  if (!hubState.detailModalOpen) {
    return;
  }
  hubState.detailModalOpen = false;
  syncDetailModal();
  window.requestAnimationFrame(() => {
    findSelectedSkillButton()?.focus();
  });
}

function findSelectedSkillButton() {
  const buttons = document.querySelectorAll("[data-select-skill]");
  for (const button of buttons) {
    if (
      button.getAttribute("data-bundle-id") === hubState.selectedBundleID &&
      button.getAttribute("data-select-skill") === hubState.selectedSkillName
    ) {
      return button;
    }
  }
  return null;
}

function navigateDetailSelection(step) {
  const records = listSkillRecords();
  if (!records.length) {
    return false;
  }

  const currentIndex = records.findIndex(
    ({ bundle, skill }) =>
      bundle.id === hubState.selectedBundleID && skill.name === hubState.selectedSkillName,
  );
  if (currentIndex === -1) {
    return false;
  }

  const nextIndex = currentIndex + step;
  if (nextIndex < 0 || nextIndex >= records.length) {
    return false;
  }

  const nextRecord = records[nextIndex];
  hubState.selectedBundleID = nextRecord.bundle.id;
  hubState.selectedSkillName = nextRecord.skill.name;
  hubState.openBundles.add(nextRecord.bundle.id);
  render();
  return true;
}

function listSkillRecords() {
  return hubState.bundles.flatMap((bundle) =>
    (bundle.skills || []).map((skill) => ({ bundle, skill })),
  );
}

function shouldIgnoreDetailKeydown(target) {
  return target instanceof HTMLElement && (
    target.isContentEditable ||
    target.tagName === "INPUT" ||
    target.tagName === "TEXTAREA" ||
    target.tagName === "SELECT"
  );
}

function findSelectedSkillRecord() {
  if (!hubState.selectedSkillName) {
    return null;
  }
  for (const bundle of hubState.bundles) {
    if (hubState.selectedBundleID && bundle.id !== hubState.selectedBundleID) {
      continue;
    }
    for (const skill of bundle.skills || []) {
      if (skill.name === hubState.selectedSkillName) {
        return { bundle, skill };
      }
    }
  }
  return null;
}

function isSelectedSkill(bundleID, skillName) {
  return hubState.selectedBundleID === bundleID && hubState.selectedSkillName === skillName;
}

function summaryCard(label, value) {
  return `
    <div class="summary-card">
      <strong>${escapeHTML(value)}</strong>
      <span>${escapeHTML(label)}</span>
    </div>
  `;
}

function setStatus(message, isError = false) {
  hubState.status = message;
  const status = document.getElementById("hubStatus");
  if (!status) {
    return;
  }
  status.textContent = message;
  status.className = `status-text ${isError ? "error" : "muted"}`;
}
