import fs from "node:fs";
import path from "node:path";
import os from "node:os";

export class SessionMap {
  private map: Record<string, string>;
  private filePath: string;

  constructor(filePath: string) {
    this.filePath = filePath;
    this.map = this.load();
  }

  get(conversationId: string): string | undefined {
    return this.map[conversationId];
  }

  set(conversationId: string, sessionId: string): void {
    this.map[conversationId] = sessionId;
    this.save();
  }

  delete(conversationId: string): void {
    delete this.map[conversationId];
    this.save();
  }

  private load(): Record<string, string> {
    try {
      const data = fs.readFileSync(this.filePath, "utf-8");
      return JSON.parse(data);
    } catch (err: any) {
      if (err.code === "ENOENT") return {};
      throw err;
    }
  }

  private save(): void {
    const dir = path.dirname(this.filePath);
    fs.mkdirSync(dir, { recursive: true });

    const tmp = this.filePath + `.tmp.${process.pid}.${Date.now()}`;
    fs.writeFileSync(tmp, JSON.stringify(this.map, null, 2) + os.EOL);
    fs.renameSync(tmp, this.filePath);
  }
}
