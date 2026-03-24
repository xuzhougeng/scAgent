import { t } from "./i18n.mjs";

export const appState = {
  sessionId: null,
  workspaceId: null,
  focusObjectId: null,
  selectedResourceKey: null,
  snapshot: null,
  workspaceSnapshot: null,
  workspaceList: [],
  workspaceStatus: "",
  skills: [],
  plugins: [],
  systemStatus: null,
  eventSource: null,
  plannerPreview: null,
  workspaceNavigatorCollapsed: false,
  sessionMetaCollapsed: false,
  workspaceFilesModalRenderVersion: 0,
  chatRenderVersion: 0,
  artifactTextCache: new Map(),
  composerPending: false,
  cancelPendingJobId: null,
  composerEditJobId: null,
  composerEditOriginalMessage: "",
};

export const storageKeys = {
  workspaceId: "scagent.workspaceId",
  sessionId: "scagent.sessionId",
  leftPanelWidth: "scagent.leftPanelWidth",
  rightPanelWidth: "scagent.rightPanelWidth",
  rightPanelCollapsed: "scagent.rightPanelCollapsed",
};

export const layoutConfig = {
  defaultLeftPanelWidth: 300,
  defaultRightPanelWidth: 360,
  minLeftPanelWidth: 260,
  minRightPanelWidth: 280,
  minConsoleWidth: 420,
  keyboardResizeStep: 24,
};

export function getQuickActions() {
  return [
    { label: t("quickAction.inspectDataset.label"), prompt: t("quickAction.inspectDataset.prompt") },
    { label: t("quickAction.preprocess.label"), prompt: t("quickAction.preprocess.prompt") },
    { label: t("quickAction.plotUmap.label"), prompt: t("quickAction.plotUmap.prompt") },
    { label: t("quickAction.subsetTcells.label"), prompt: t("quickAction.subsetTcells.prompt") },
    { label: t("quickAction.recluster.label"), prompt: t("quickAction.recluster.prompt") },
    { label: t("quickAction.findMarkers.label"), prompt: t("quickAction.findMarkers.prompt") },
    { label: t("quickAction.exportH5ad.label"), prompt: t("quickAction.exportH5ad.prompt") },
  ];
}
