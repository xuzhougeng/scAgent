import type { Agent, ChatRequest, ChatResponse } from "weixin-agent-sdk";
import type { BridgeConfig } from "./config.js";
import { ScAgentClient } from "./scagent-client.js";
import { SessionMap } from "./session-map.js";

export class ScAgentBridge implements Agent {
  private client: ScAgentClient;
  private sessions: SessionMap;
  private config: BridgeConfig;

  constructor(config: BridgeConfig) {
    this.config = config;
    this.client = new ScAgentClient(config.scagentBaseUrl, config.jobTimeoutMs);
    this.sessions = new SessionMap(config.sessionMapPath);
  }

  async chat(request: ChatRequest): Promise<ChatResponse> {
    const { conversationId, text } = request;

    if (!text?.trim()) {
      return { text: "Please send a text message." };
    }

    // Resolve or create scAgent session
    const sessionId = await this.resolveSession(conversationId);

    console.log(
      `[bridge] ${conversationId} → session ${sessionId}: ${text.slice(0, 80)}`,
    );

    // Submit message — if session is stale (server restarted), retry with new session
    let submitResult;
    try {
      submitResult = await this.client.submitMessage(sessionId, text);
    } catch (err: any) {
      console.log(
        `[bridge] submit failed for ${sessionId}, recreating session: ${err.message}`,
      );
      this.sessions.delete(conversationId);
      const newSessionId = await this.resolveSession(conversationId);
      submitResult = await this.client.submitMessage(newSessionId, text);
    }

    const { job } = submitResult;
    const result = await this.client.waitForCompletion(
      job.session_id,
      job.id,
    );

    // Build response — send first plot as image if available
    if (result.plotUrls.length > 0) {
      let responseText = result.text;
      if (result.plotUrls.length > 1) {
        responseText += `\n\n(${result.plotUrls.length} plots generated, showing the first)`;
      }
      return {
        text: responseText,
        media: { type: "image", url: result.plotUrls[0] },
      };
    }

    return { text: result.text };
  }

  /**
   * Resolve a WeChat conversationId to a scAgent session.
   * - If a cached mapping exists, use it
   * - Otherwise, find the most recently accessed workspace and create
   *   a conversation in it (reuses existing data)
   * - If no workspaces exist, create a brand new session+workspace
   */
  private async resolveSession(conversationId: string): Promise<string> {
    const cached = this.sessions.get(conversationId);
    if (cached) return cached;

    const label = `${this.config.defaultSessionLabel}-${conversationId.slice(0, 8)}`;

    // Try to reuse the most recent workspace
    const workspaces = await this.client.listWorkspaces();
    if (workspaces.length > 0) {
      // Pick the most recently accessed workspace
      workspaces.sort(
        (a, b) =>
          new Date(b.last_accessed_at).getTime() -
          new Date(a.last_accessed_at).getTime(),
      );
      const ws = workspaces[0];
      console.log(
        `[bridge] new conversation ${conversationId} → joining workspace ${ws.id} ("${ws.label}")`,
      );
      const snapshot = await this.client.createConversation(ws.id, label);
      const sessionId = snapshot.session.id;
      this.sessions.set(conversationId, sessionId);
      return sessionId;
    }

    // No workspaces — create fresh
    console.log(
      `[bridge] new conversation ${conversationId} → creating session "${label}"`,
    );
    const snapshot = await this.client.createSession(label);
    const sessionId = snapshot.session.id;
    this.sessions.set(conversationId, sessionId);
    return sessionId;
  }
}
