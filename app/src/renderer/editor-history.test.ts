import { describe, expect, it } from "vitest";
import type { Profile } from "../shared/generated/schema";
import { EditHistory, HISTORY_LIMIT } from "./editor-history";

const named = (name: string): Profile => ({
  schemaVersion: 1,
  name,
  layers: { base: {} },
});

describe("edit history", () => {
  it("undoes and redoes whole drafts in order", () => {
    const history = new EditHistory();
    expect(history.canUndo).toBe(false);
    expect(history.undo(named("a"))).toBeNull();
    history.record(named("a"));
    history.record(named("b"));
    expect(history.undo(named("c"))?.name).toBe("b");
    expect(history.undo(named("b"))?.name).toBe("a");
    expect(history.canUndo).toBe(false);
    expect(history.redo(named("a"))?.name).toBe("b");
    expect(history.redo(named("b"))?.name).toBe("c");
    expect(history.canRedo).toBe(false);
    expect(history.redo(named("c"))).toBeNull();
  });

  it("drops redo after a new step", () => {
    const history = new EditHistory();
    history.record(named("a"));
    history.undo(named("b"));
    expect(history.canRedo).toBe(true);
    history.record(named("a"));
    expect(history.canRedo).toBe(false);
  });

  it("keeps at most the limit, dropping the oldest", () => {
    const history = new EditHistory();
    for (let i = 0; i <= HISTORY_LIMIT; i++) history.record(named(`${i}`));
    let oldest = "";
    let steps = 0;
    let current = named("now");
    for (;;) {
      const previous = history.undo(current);
      if (!previous) break;
      oldest = previous.name;
      current = previous;
      steps++;
    }
    expect(steps).toBe(HISTORY_LIMIT);
    expect(oldest).toBe("1");
  });

  it("keeps copies, so a later change to a draft does not reach it", () => {
    const history = new EditHistory();
    const draft = named("a");
    history.record(draft);
    draft.name = "changed";
    expect(history.undo(named("b"))?.name).toBe("a");
  });

  it("clears both directions", () => {
    const history = new EditHistory();
    history.record(named("a"));
    history.record(named("b"));
    history.undo(named("c"));
    history.clear();
    expect(history.canUndo).toBe(false);
    expect(history.canRedo).toBe(false);
  });
});
