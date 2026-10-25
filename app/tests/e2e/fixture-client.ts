import { readFile } from "node:fs/promises";
import { createConnection, type Socket } from "node:net";
import { join } from "node:path";
import type { Fixture } from "./helpers";

export interface ObservedEvent {
  topic: string;
  data: Record<string, unknown>;
  at: number;
}

// Only used against a fixture's randomly named test pipe. Production clients
// keep using the Go bridge and its Windows pipe-server PID verification.
export class FixtureClient {
  readonly events: ObservedEvent[] = [];
  onEvent?: (event: ObservedEvent) => void;
  private nextID = 0;
  private pending = new Map<
    number,
    {
      resolve(value: unknown): void;
      reject(error: Error): void;
      timeout: ReturnType<typeof setTimeout>;
    }
  >();

  private constructor(private socket: Socket) {
    let buffer = "";
    socket.setEncoding("utf8");
    socket.on("data", (chunk: string) => {
      buffer += chunk;
      if (buffer.length > 8 * 1024 * 1024) {
        socket.destroy(new Error("Test IPC frame exceeds limit"));
        return;
      }
      let end = buffer.indexOf("\n");
      while (end >= 0) {
        const line = buffer.slice(0, end);
        buffer = buffer.slice(end + 1);
        this.receive(JSON.parse(line));
        end = buffer.indexOf("\n");
      }
    });
    socket.on("error", (error) => this.rejectAll(error));
    socket.on("close", () => this.rejectAll(new Error("Test IPC closed")));
  }

  static async connect(f: Fixture): Promise<FixtureClient> {
    const discovery = JSON.parse(
      await readFile(join(f.ipc, "daemon.json"), "utf8"),
    );
    if (
      discovery.pipe !== f.env.PADMATE_M10_PIPE ||
      !discovery.pipe.startsWith("\\\\.\\pipe\\padmate-m10-e2e-")
    )
      throw new Error("Refusing a non-fixture IPC endpoint");
    const socket = createConnection(discovery.pipe);
    const client = new FixtureClient(socket);
    try {
      await client.call("hello", {
        protocol: discovery.protocol,
        token: discovery.token,
        client: "editor-e2e",
      });
      await client.call("subscribe", {
        topics: [
          "profile.changed",
          "output.preview",
          "input",
          "engine.activity",
          "log",
        ],
      });
      return client;
    } catch (error) {
      client.close();
      throw error;
    }
  }

  call<T = unknown>(method: string, params: unknown = {}): Promise<T> {
    const id = ++this.nextID;
    return new Promise<T>((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`Test RPC ${method} timed out`));
      }, 5000);
      this.pending.set(id, {
        resolve: (value) => resolve(value as T),
        reject,
        timeout,
      });
      this.socket.write(
        `${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`,
      );
    });
  }

  close(): void {
    this.socket.destroy();
    this.rejectAll(new Error("Test IPC closed"));
  }

  private receive(message: {
    id?: number;
    result?: unknown;
    error?: { message: string; code: number };
    method?: string;
    params?: Record<string, unknown>;
  }): void {
    if (typeof message.id === "number") {
      const request = this.pending.get(message.id);
      if (!request) return;
      this.pending.delete(message.id);
      clearTimeout(request.timeout);
      if (message.error)
        request.reject(
          new Error(`${message.error.code}: ${message.error.message}`),
        );
      else request.resolve(message.result);
    } else if (message.method) {
      const event = {
        topic: message.method,
        data: message.params ?? {},
        at: performance.now(),
      };
      this.events.push(event);
      if (this.events.length > 2048) this.events.shift();
      this.onEvent?.(event);
    }
  }

  private rejectAll(error: Error): void {
    for (const request of this.pending.values()) {
      clearTimeout(request.timeout);
      request.reject(error);
    }
    this.pending.clear();
  }
}
