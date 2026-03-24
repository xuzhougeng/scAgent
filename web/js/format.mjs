import { appState } from "./state.mjs";
import { t, tLabel } from "./i18n.mjs";

export function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

export function escapeAttribute(value) {
  return escapeHTML(value).replaceAll('"', "&quot;");
}

export function translateLabel(value, domain, fallback) {
  if (value === null || value === undefined || value === "") {
    return fallback || t("ui.unknown");
  }
  return tLabel(value, domain, fallback);
}

export function formatList(values) {
  if (!values || !values.length) {
    return t("ui.none");
  }
  return values.join(t("ui.listSeparator"));
}

export function formatMemoryValue(value) {
  if (value === null || value === undefined || value === "") {
    return t("ui.none");
  }
  if (Array.isArray(value)) {
    return value.join(t("ui.listSeparator"));
  }
  if (typeof value === "object") {
    return JSON.stringify(value);
  }
  return String(value);
}

export function formatSkillList(values) {
  if (!values || !values.length) {
    return t("ui.none");
  }
  return values.map((value) => formatSkillName(value)).join(t("ui.listSeparator"));
}

export function objectLabel(objectId) {
  const object = (appState.snapshot?.objects || []).find((item) => item.id === objectId);
  return object ? object.label : objectId;
}

export function formatPlanTarget(targetObjectId) {
  if (!targetObjectId || targetObjectId === "$active") {
    return t("ui.currentObject");
  }
  if (targetObjectId === "$prev") {
    return t("ui.prevOutput");
  }
  return objectLabel(targetObjectId);
}

export function formatRole(role) {
  return tLabel(role, "role", role || t("ui.unknown"));
}

export function formatConversationLabel(session) {
  if (!session) {
    return t("ui.unnamedConversation");
  }
  return session.label || session.id || t("ui.unnamedConversation");
}

export function formatSkillName(skill) {
  return tLabel(skill, "skill", skill || t("ui.unknownSkill"));
}

export function promptForSkill(skill) {
  const key = `skill.prompt.${skill}`;
  const result = t(key);
  return result !== key ? result : skill || "";
}

export function formatJobStatus(status) {
  return tLabel(status, "jobStatus", status || t("ui.unknown"));
}

export function formatSystemMode(mode) {
  return tLabel(mode, "systemMode", mode || t("ui.unknown"));
}

export function formatPlannerMode(mode) {
  return tLabel(mode, "plannerMode", mode || t("ui.unknown"));
}

export function formatRuntimeMode(mode) {
  return tLabel(mode, "runtimeMode", mode || t("ui.unknown"));
}

export function formatObjectKind(kind) {
  return tLabel(kind, "objectKind", kind || t("ui.unknownType"));
}

export function formatObjectState(state) {
  return tLabel(state, "objectState", state || t("ui.unknown"));
}

export function formatArtifactKind(kind) {
  return tLabel(kind, "artifactKind", kind || t("ui.unknownType"));
}

export function formatAnnotationRole(role) {
  return tLabel(role, "annotationRole", role || t("ui.annotation"));
}

export function formatAnalysisState(state) {
  return tLabel(state, "analysisState", state || t("ui.unknown"));
}

export function formatAnnotation(annotation) {
  if (!annotation) {
    return t("ui.unrecognized");
  }
  const sample = (annotation.sample_values || []).slice(0, 4).join(t("ui.listSeparator"));
  return `${annotation.field} · ${annotation.n_categories} ${t("ui.groups")}${sample ? ` · ${sample}` : ""}`;
}

export function statusPill(kind, label) {
  return `<span class="status-pill ${kind}">${escapeHTML(label)}</span>`;
}

export function statusKindForJob(status) {
  switch (status) {
    case "succeeded":
      return "ok";
    case "incomplete":
      return "warn";
    case "canceled":
      return "muted";
    case "failed":
      return "bad";
    case "running":
      return "warn";
    default:
      return "muted";
  }
}

export function formatJobPhaseStatus(status) {
  return tLabel(status, "jobPhaseStatus", status || t("ui.unknown"));
}

export function formatJobPhaseKind(kind) {
  switch (kind) {
    case "decide":
      return t("phase.decide");
    case "investigate":
      return t("phase.investigate");
    case "respond":
      return t("phase.respond");
    default:
      return kind || t("phase.default");
  }
}

export function statusKindForPhase(status) {
  switch (status) {
    case "completed":
      return "ok";
    case "running":
      return "warn";
    case "canceled":
      return "muted";
    case "failed":
      return "error";
    case "skipped":
      return "muted";
    default:
      return "muted";
  }
}

export function normalizeCheckpointTone(tone) {
  switch (tone) {
    case "ok":
    case "warn":
    case "bad":
    case "muted":
      return tone;
    default:
      return "muted";
  }
}
