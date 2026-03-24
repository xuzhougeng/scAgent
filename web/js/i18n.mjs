const STORAGE_KEY = "scagent.locale";
const DEFAULT_LOCALE = "zh";

let currentLocale = DEFAULT_LOCALE;
let messages = {};

export async function initI18n(locale) {
  const lang = locale || loadStoredLocale() || DEFAULT_LOCALE;
  await loadLocale(lang);
}

export async function setLocale(locale) {
  await loadLocale(locale);
  document.documentElement.lang = locale === "zh" ? "zh-CN" : locale;
  translateDOM();
}

export function getLocale() {
  return currentLocale;
}

export function t(key, params) {
  let template = messages[key];
  if (template === undefined || template === null) {
    return key;
  }
  if (!params) {
    return template;
  }
  return template.replace(/\{(\w+)\}/g, (_match, name) => {
    const value = params[name];
    return value !== undefined && value !== null ? String(value) : `{${name}}`;
  });
}

export function tLabel(value, domain, fallback) {
  if (value === null || value === undefined || value === "") {
    return fallback || t("ui.unknown");
  }
  const key = `${domain}.${value}`;
  const result = messages[key];
  if (result !== undefined && result !== null) {
    return result;
  }
  return String(value);
}

export function translateDOM() {
  for (const el of document.querySelectorAll("[data-i18n]")) {
    el.textContent = t(el.dataset.i18n);
  }
  for (const el of document.querySelectorAll("[data-i18n-placeholder]")) {
    el.placeholder = t(el.dataset.i18nPlaceholder);
  }
  for (const el of document.querySelectorAll("[data-i18n-aria-label]")) {
    el.setAttribute("aria-label", t(el.dataset.i18nAriaLabel));
  }
  for (const el of document.querySelectorAll("[data-i18n-title]")) {
    el.title = t(el.dataset.i18nTitle);
  }
  for (const el of document.querySelectorAll("[data-i18n-html]")) {
    el.innerHTML = t(el.dataset.i18nHtml);
  }
}

async function loadLocale(locale) {
  try {
    const response = await fetch(`/locales/${locale}.json`);
    if (!response.ok) {
      throw new Error(`Failed to load locale: ${response.status}`);
    }
    messages = await response.json();
    currentLocale = locale;
    storeLocale(locale);
  } catch (error) {
    console.warn(`[i18n] Failed to load locale "${locale}":`, error);
    if (locale !== DEFAULT_LOCALE) {
      await loadLocale(DEFAULT_LOCALE);
    }
  }
}

function loadStoredLocale() {
  try {
    return window.localStorage.getItem(STORAGE_KEY) || "";
  } catch {
    return "";
  }
}

function storeLocale(locale) {
  try {
    window.localStorage.setItem(STORAGE_KEY, locale);
  } catch {
    // ignore
  }
}
