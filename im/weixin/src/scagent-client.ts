interface Job {
  id: string;
  session_id: string;
  status: "queued" | "running" | "succeeded" | "failed";
  error?: string;
  summary?: string;
}

interface Artifact {
  id: string;
  job_id?: string;
  kind: "plot" | "table" | "object_summary" | "file";
  title: string;
  url: string;
  content_type: string;
}

interface Message {
  id: string;
  session_id: string;
  role: "user" | "assistant" | "system";
  content: string;
  job_id?: string;
}

interface Workspace {
  id: string;
  label: string;
  dataset_id: string;
  active_object_id: string;
  last_accessed_at: string;
}

interface SessionSnapshot {
  session: { id: string; workspace_id?: string; label: string };
  workspace?: Workspace;
  jobs: Job[];
  artifacts: Artifact[];
  messages: Message[];
}

interface WorkspaceList {
  workspaces: Workspace[];
}

interface SubmitResult {
  job: Job;
  snapshot: SessionSnapshot;
}

export interface CompletionResult {
  text: string;
  plotUrls: string[];
}

export class ScAgentClient {
  constructor(
    private baseUrl: string,
    private timeoutMs: number,
  ) {}

  async listWorkspaces(): Promise<Workspace[]> {
    const res = await fetch(`${this.baseUrl}/api/workspaces`);
    if (!res.ok) return [];
    const data = (await res.json()) as WorkspaceList;
    return data.workspaces ?? [];
  }

  async createSession(label: string): Promise<SessionSnapshot> {
    const res = await fetch(`${this.baseUrl}/api/sessions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ label }),
    });
    if (!res.ok) {
      throw new Error(`createSession failed: ${res.status} ${await res.text()}`);
    }
    return res.json() as Promise<SessionSnapshot>;
  }

  async createConversation(
    workspaceId: string,
    label: string,
  ): Promise<SessionSnapshot> {
    const res = await fetch(
      `${this.baseUrl}/api/workspaces/${workspaceId}/conversations`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ label }),
      },
    );
    if (!res.ok) {
      throw new Error(
        `createConversation failed: ${res.status} ${await res.text()}`,
      );
    }
    return res.json() as Promise<SessionSnapshot>;
  }

  async submitMessage(
    sessionId: string,
    message: string,
  ): Promise<SubmitResult> {
    const res = await fetch(`${this.baseUrl}/api/messages`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ session_id: sessionId, message }),
    });
    if (!res.ok) {
      throw new Error(
        `submitMessage failed: ${res.status} ${await res.text()}`,
      );
    }
    return res.json() as Promise<SubmitResult>;
  }

  async waitForCompletion(
    sessionId: string,
    jobId: string,
  ): Promise<CompletionResult> {
    const ac = new AbortController();
    const timer = setTimeout(() => ac.abort(), this.timeoutMs);

    try {
      const res = await fetch(
        `${this.baseUrl}/api/sessions/${sessionId}/events`,
        { headers: { Accept: "text/event-stream" }, signal: ac.signal },
      );
      if (!res.ok || !res.body) {
        throw new Error(`SSE connect failed: ${res.status}`);
      }

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";
      let eventType = "";

      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });

        // Process complete lines
        let nlIdx: number;
        while ((nlIdx = buf.indexOf("\n")) !== -1) {
          const line = buf.slice(0, nlIdx).replace(/\r$/, "");
          buf = buf.slice(nlIdx + 1);

          if (line.startsWith("event: ")) {
            eventType = line.slice(7).trim();
          } else if (line.startsWith("data: ") && eventType === "session_updated") {
            const data = line.slice(6);
            let snapshot: SessionSnapshot;
            try {
              snapshot = JSON.parse(data);
            } catch {
              continue;
            }

            const job = snapshot.jobs?.find((j) => j.id === jobId);
            if (!job) continue;
            if (job.status !== "succeeded" && job.status !== "failed") continue;

            clearTimeout(timer);
            reader.cancel();

            if (job.status === "failed") {
              return {
                text: `Analysis failed: ${job.error || "unknown error"}`,
                plotUrls: [],
              };
            }

            const assistantMsg = [...(snapshot.messages ?? [])]
              .reverse()
              .find((m) => m.job_id === jobId && m.role === "assistant");

            const plots = (snapshot.artifacts ?? [])
              .filter((a) => a.job_id === jobId && a.kind === "plot")
              .map((a) => `${this.baseUrl}${a.url}`);

            return {
              text: assistantMsg?.content ?? job.summary ?? "Done.",
              plotUrls: plots,
            };
          } else if (line === "") {
            eventType = "";
          }
        }
      }

      throw new Error("SSE stream ended before job completed");
    } catch (err: any) {
      if (err.name === "AbortError") {
        throw new Error(`Job ${jobId} timed out after ${this.timeoutMs}ms`);
      }
      throw err;
    } finally {
      clearTimeout(timer);
    }
  }
}
