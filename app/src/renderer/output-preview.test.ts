import { describe, expect, it } from "vitest";
import { displayChord } from "./output-preview";

describe("output preview text", () => {
  it("uses familiar names for the standard left modifiers", () => {
    expect(displayChord("LCtrl+Z")).toBe("Ctrl+Z");
    expect(displayChord("LWin+LAlt+LShift+F13")).toBe("Win+Alt+Shift+F13");
  });

  it("preserves right modifier distinctions and other key names", () => {
    expect(displayChord("RCtrl+RAlt+L")).toBe("RCtrl+RAlt+L");
    expect(displayChord("LCtrl++")).toBe("Ctrl++");
  });
});
