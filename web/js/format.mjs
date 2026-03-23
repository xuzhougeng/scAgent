import {
  analysisStateLabels,
  annotationRoleLabels,
  appState,
  jobPhaseStatusLabels,
  jobStatusLabels,
  objectKindLabels,
  objectStateLabels,
  plannerModeLabels,
  roleLabels,
  runtimeModeLabels,
  skillLabels,
  skillPrompts,
  systemModeLabels,
} from "./state.mjs";

export function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

export function escapeAttribute(value) {
  return escapeHTML(value).replaceAll('"', "&quot;");
}

export function translateLabel(value, labels, fallback = "未知") {
  if (value === null || value === undefined || value === "") {
    return fallback;
  }
  return labels[value] || String(value);
}

export function formatList(values) {
  if (!values || !values.length) {
    return "无";
  }
  return values.join("、");
}

export function formatMemoryValue(value) {
  if (value === null || value === undefined || value === "") {
    return "无";
  }
  if (Array.isArray(value)) {
    return value.join("、");
  }
  if (typeof value === "object") {
    return JSON.stringify(value);
  }
  return String(value);
}

export function formatSkillList(values) {
  if (!values || !values.length) {
    return "无";
  }
  return values.map((value) => formatSkillName(value)).join("、");
}

export function objectLabel(objectId) {
  const object = (appState.snapshot?.objects || []).find((item) => item.id === objectId);
  return object ? object.label : objectId;
}

export function formatPlanTarget(targetObjectId) {
  if (!targetObjectId || targetObjectId === "$active") {
    return "当前对象";
  }
  if (targetObjectId === "$prev") {
    return "上一步输出";
  }
  return objectLabel(targetObjectId);
}

export function formatRole(role) {
  return roleLabels[role] || role || "未知";
}

export function formatConversationLabel(session) {
  if (!session) {
    return "未命名对话";
  }
  return session.label || session.id || "未命名对话";
}

export function formatSkillName(skill) {
  return translateLabel(skill, skillLabels, skill || "未知技能");
}

export function promptForSkill(skill) {
  return skillPrompts[skill] || skill || "";
}

export function formatJobStatus(status) {
  return translateLabel(status, jobStatusLabels, status || "未知");
}

export function formatSystemMode(mode) {
  return translateLabel(mode, systemModeLabels, mode || "未知");
}

export function formatPlannerMode(mode) {
  return translateLabel(mode, plannerModeLabels, mode || "未知");
}

export function formatRuntimeMode(mode) {
  return translateLabel(mode, runtimeModeLabels, mode || "未知");
}

export function formatObjectKind(kind) {
  return translateLabel(kind, objectKindLabels, kind || "未知类型");
}

export function formatObjectState(state) {
  return translateLabel(state, objectStateLabels, state || "未知");
}

export function formatAnnotationRole(role) {
  return translateLabel(role, annotationRoleLabels, role || "注释");
}

export function formatAnalysisState(state) {
  return translateLabel(state, analysisStateLabels, state || "未知");
}

export function formatAnnotation(annotation) {
  if (!annotation) {
    return "未识别";
  }
  const sample = (annotation.sample_values || []).slice(0, 4).join("、");
  return `${annotation.field} · ${annotation.n_categories} 组${sample ? ` · ${sample}` : ""}`;
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
  return translateLabel(status, jobPhaseStatusLabels, status || "未知");
}

export function formatJobPhaseKind(kind) {
  switch (kind) {
    case "decide":
      return "快速判断";
    case "investigate":
      return "信息收集";
    case "respond":
      return "确认与回答";
    default:
      return kind || "阶段";
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
