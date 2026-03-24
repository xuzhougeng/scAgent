import { t, getLocale } from "./i18n.mjs";

export async function fetchJSON(url, options) {
  const merged = { ...options };
  merged.headers = new Headers(merged.headers || {});
  if (!merged.headers.has("Accept-Language")) {
    merged.headers.set("Accept-Language", getLocale());
  }

  const response = await fetch(url, merged);
  const contentType = response.headers.get("Content-Type") || "";

  if (!response.ok) {
    let message = "";
    if (contentType.includes("application/json")) {
      const payload = await response.json().catch(() => null);
      message = payload?.error || payload?.message || "";
    } else {
      message = await response.text();
    }
    throw new Error(message || t("ui.requestFailed", { status: response.status }));
  }

  if (contentType.includes("application/json")) {
    return response.json();
  }
  return null;
}
