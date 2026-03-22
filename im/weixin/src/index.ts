import { login, start } from "weixin-agent-sdk";
import { loadConfig } from "./config.js";
import { ScAgentBridge } from "./agent.js";

const subcommand = process.argv[2];

async function runLogin() {
  console.log("[bridge] starting WeChat QR login...");
  const accountId = await login();
  console.log(`[bridge] login succeeded, account: ${accountId}`);
}

async function runBridge() {
  const config = loadConfig();

  console.log("[bridge] scAgent WeChat bridge starting");
  console.log(`  scAgent URL:  ${config.scagentBaseUrl}`);
  console.log(`  session map:  ${config.sessionMapPath}`);
  console.log(`  job timeout:  ${config.jobTimeoutMs}ms`);

  // Verify scAgent is reachable
  try {
    const res = await fetch(`${config.scagentBaseUrl}/healthz`);
    if (!res.ok) throw new Error(`status ${res.status}`);
  } catch (err) {
    console.error(
      `[bridge] scAgent not reachable at ${config.scagentBaseUrl}:`,
      err,
    );
    process.exit(1);
  }
  console.log("[bridge] scAgent is healthy");

  const agent = new ScAgentBridge(config);
  await start(agent);
}

async function main() {
  if (subcommand === "login") {
    await runLogin();
  } else {
    await runBridge();
  }
}

main().catch((err) => {
  console.error("[bridge] fatal:", err);
  process.exit(1);
});
