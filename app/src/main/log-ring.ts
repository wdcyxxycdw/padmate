const MAX_LINE = 500;

/**
 * The log of electron-updater, kept in memory: the latest lines, nothing on
 * disk and nothing sent anywhere (PKG-10). It has the interface the updater
 * wants of a logger.
 */
export class LogRing {
  private readonly entries: string[] = [];

  constructor(
    private readonly capacity = 200,
    private readonly now: () => number = Date.now,
  ) {}

  info(message?: unknown): void {
    this.add("INFO", message);
  }

  warn(message?: unknown): void {
    this.add("WARN", message);
  }

  error(message?: unknown): void {
    this.add("ERROR", message);
  }

  debug(message?: unknown): void {
    this.add("DEBUG", message);
  }

  /** The lines kept, oldest first. */
  lines(): string[] {
    return [...this.entries];
  }

  private add(level: string, message: unknown): void {
    let text = describe(message).replace(/\s*[\r\n]+\s*/g, " ");
    if (text.length > MAX_LINE) text = `${text.slice(0, MAX_LINE - 1)}…`;
    this.entries.push(`${new Date(this.now()).toISOString()} ${level} ${text}`);
    if (this.entries.length > this.capacity)
      this.entries.splice(0, this.entries.length - this.capacity);
  }
}

function describe(message: unknown): string {
  if (message === undefined || message === null) return "";
  if (typeof message === "string") return message;
  if (message instanceof Error) return `${message.name}: ${message.message}`;
  try {
    return JSON.stringify(message) ?? String(message);
  } catch {
    return String(message);
  }
}
