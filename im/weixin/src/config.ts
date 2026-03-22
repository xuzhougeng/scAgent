import path from "node:path";
import { fileURLToPath } from "node:url";

export interface BridgeConfig {
  scagentBaseUrl: string;
  dataDir: string;
  sessionMapPath: string;
  jobTimeoutMs: number;
  defaultSessionLabel: string;
}

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const PROJECT_ROOT = path.resolve(__dirname, "..", "..", "..");

export function loadConfig(): BridgeConfig {
  const dataDir = path.resolve(
    PROJECT_ROOT,
    process.env.SCAGENT_DATA_DIR ?? "data",
  );
  const scagentBaseUrl = (
    process.env.SCAGENT_BASE_URL ?? "http://127.0.0.1:8080"
  ).replace(/\/+$/, "");
  const jobTimeoutMs = parseInt(
    process.env.WEIXIN_BRIDGE_TIMEOUT_MS ?? "300000",
    10,
  );
  const defaultSessionLabel =
    process.env.WEIXIN_BRIDGE_SESSION_LABEL ?? "WeChat";
  const sessionMapPath = path.join(dataDir, "weixin-bridge", "sessions.json");

  return Object.freeze({
    scagentBaseUrl,
    dataDir,
    sessionMapPath,
    jobTimeoutMs,
    defaultSessionLabel,
  });
}
