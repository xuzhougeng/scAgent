import { layoutConfig, storageKeys } from "./state.mjs";
import { t } from "./i18n.mjs";

export function bindSidebarResize() {
  const shell = document.querySelector(".shell");
  const leftHandle = document.getElementById("leftSidebarHandle");
  const rightHandle = document.getElementById("rightSidebarHandle");
  const rightToggle = document.getElementById("rightSidebarToggle");
  if (!shell || !leftHandle || !rightHandle || !rightToggle) {
    return;
  }

  applySidebarWidths(shell, restoreSidebarWidths(), false);
  setRightSidebarCollapsed(shell, restoreRightSidebarCollapsed(), false);

  bindSidebarHandle({
    shell,
    handle: leftHandle,
    side: "left",
  });
  bindSidebarHandle({
    shell,
    handle: rightHandle,
    side: "right",
  });

  rightToggle.addEventListener("click", () => {
    setRightSidebarCollapsed(shell, !isRightSidebarCollapsed(shell), true);
  });

  window.addEventListener("resize", () => {
    applySidebarWidths(shell, readSidebarWidths(shell), false);
    if (window.matchMedia("(max-width: 1100px)").matches) {
      setRightSidebarCollapsed(shell, false, false);
    } else {
      setRightSidebarCollapsed(shell, restoreRightSidebarCollapsed(), false);
    }
  });
}

function bindSidebarHandle({ shell, handle, side }) {
  handle.addEventListener("pointerdown", (event) => {
    if (window.matchMedia("(max-width: 1100px)").matches) {
      return;
    }
    if (event.target.closest(".sidebar-collapse-button")) {
      return;
    }
    if (side === "right" && isRightSidebarCollapsed(shell)) {
      return;
    }

    event.preventDefault();
    const startX = event.clientX;
    const startWidths = readSidebarWidths(shell);

    document.body.classList.add("is-resizing");
    handle.classList.add("active");
    handle.setPointerCapture?.(event.pointerId);

    const onPointerMove = (moveEvent) => {
      const delta = moveEvent.clientX - startX;
      const nextWidths =
        side === "left"
          ? { ...startWidths, left: startWidths.left + delta }
          : { ...startWidths, right: startWidths.right - delta };
      applySidebarWidths(shell, nextWidths, true);
    };

    const stopResize = () => {
      document.body.classList.remove("is-resizing");
      handle.classList.remove("active");
      window.removeEventListener("pointermove", onPointerMove);
      window.removeEventListener("pointerup", stopResize);
      window.removeEventListener("pointercancel", stopResize);
    };

    window.addEventListener("pointermove", onPointerMove);
    window.addEventListener("pointerup", stopResize);
    window.addEventListener("pointercancel", stopResize);
  });

  handle.addEventListener("keydown", (event) => {
    if (window.matchMedia("(max-width: 1100px)").matches) {
      return;
    }
    if (side === "right" && isRightSidebarCollapsed(shell)) {
      return;
    }

    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") {
      return;
    }

    const delta = event.key === "ArrowLeft" ? -layoutConfig.keyboardResizeStep : layoutConfig.keyboardResizeStep;
    const widths = readSidebarWidths(shell);
    const nextWidths =
      side === "left"
        ? { ...widths, left: widths.left + delta }
        : { ...widths, right: widths.right - delta };

    applySidebarWidths(shell, nextWidths, true);
    event.preventDefault();
  });
}

function restoreSidebarWidths() {
  try {
    const left = parseStoredWidth(window.localStorage.getItem(storageKeys.leftPanelWidth));
    const right = parseStoredWidth(window.localStorage.getItem(storageKeys.rightPanelWidth));
    return {
      left: left || layoutConfig.defaultLeftPanelWidth,
      right: right || layoutConfig.defaultRightPanelWidth,
    };
  } catch (_error) {
    return {
      left: layoutConfig.defaultLeftPanelWidth,
      right: layoutConfig.defaultRightPanelWidth,
    };
  }
}

function restoreRightSidebarCollapsed() {
  try {
    return window.localStorage.getItem(storageKeys.rightPanelCollapsed) === "true";
  } catch (_error) {
    return false;
  }
}

function readSidebarWidths(shell) {
  const styles = getComputedStyle(shell);
  return {
    left: parseCSSPixelValue(styles.getPropertyValue("--left-panel-width"), layoutConfig.defaultLeftPanelWidth),
    right: parseCSSPixelValue(styles.getPropertyValue("--right-panel-width"), layoutConfig.defaultRightPanelWidth),
  };
}

function applySidebarWidths(shell, widths, persist = false) {
  const nextWidths = clampSidebarWidths(shell, widths);
  shell.style.setProperty("--left-panel-width", `${nextWidths.left}px`);
  shell.style.setProperty("--right-panel-width", `${nextWidths.right}px`);

  if (!persist) {
    return;
  }

  try {
    window.localStorage.setItem(storageKeys.leftPanelWidth, String(Math.round(nextWidths.left)));
    window.localStorage.setItem(storageKeys.rightPanelWidth, String(Math.round(nextWidths.right)));
  } catch (_error) {
  }
}

function isRightSidebarCollapsed(shell) {
  return shell.classList.contains("right-sidebar-collapsed");
}

function setRightSidebarCollapsed(shell, collapsed, persist = false) {
  const shouldCollapse = !window.matchMedia("(max-width: 1100px)").matches && collapsed;
  shell.classList.toggle("right-sidebar-collapsed", shouldCollapse);
  syncRightSidebarToggle(shell);

  if (!persist) {
    return;
  }

  try {
    window.localStorage.setItem(storageKeys.rightPanelCollapsed, String(shouldCollapse));
  } catch (_error) {
  }
}

function syncRightSidebarToggle(shell) {
  const button = document.getElementById("rightSidebarToggle");
  if (!button) {
    return;
  }

  const collapsed = isRightSidebarCollapsed(shell);
  button.textContent = collapsed ? "<" : ">";
  button.setAttribute("aria-expanded", collapsed ? "false" : "true");
  button.setAttribute("aria-label", collapsed ? t("html.expandRightPanel") : t("html.collapseRightPanel"));
  button.title = collapsed ? t("html.expandRightPanel") : t("html.collapseRightPanel");
}

function clampSidebarWidths(shell, widths) {
  const styles = getComputedStyle(shell);
  const paddingLeft = parseFloat(styles.paddingLeft) || 0;
  const paddingRight = parseFloat(styles.paddingRight) || 0;
  const contentWidth = shell.clientWidth - paddingLeft - paddingRight;
  const handleWidth = parseCSSPixelValue(styles.getPropertyValue("--resize-handle-width"), 12);
  const usableWidth = Math.max(0, contentWidth - handleWidth * 2);

  let left = clampNumber(widths.left, layoutConfig.minLeftPanelWidth, usableWidth);
  let right = clampNumber(widths.right, layoutConfig.minRightPanelWidth, usableWidth);

  const maxLeft = Math.max(
    layoutConfig.minLeftPanelWidth,
    usableWidth - layoutConfig.minConsoleWidth - right,
  );
  left = clampNumber(left, layoutConfig.minLeftPanelWidth, maxLeft);

  const maxRight = Math.max(
    layoutConfig.minRightPanelWidth,
    usableWidth - layoutConfig.minConsoleWidth - left,
  );
  right = clampNumber(right, layoutConfig.minRightPanelWidth, maxRight);

  const finalMaxLeft = Math.max(
    layoutConfig.minLeftPanelWidth,
    usableWidth - layoutConfig.minConsoleWidth - right,
  );
  left = clampNumber(left, layoutConfig.minLeftPanelWidth, finalMaxLeft);

  return { left, right };
}

function parseStoredWidth(value) {
  const width = Number.parseFloat(value || "");
  return Number.isFinite(width) && width > 0 ? width : 0;
}

function parseCSSPixelValue(value, fallback) {
  const width = Number.parseFloat(String(value || "").trim());
  return Number.isFinite(width) ? width : fallback;
}

function clampNumber(value, min, max) {
  return Math.min(Math.max(value, min), max);
}
