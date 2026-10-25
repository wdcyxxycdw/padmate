import { mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { Profile, ProfileBinding } from "../shared/generated/schema";
import { calloutFor } from "./ControllerWorkspace";
import { controllerCallouts } from "./controller-callouts";
import {
  gestures,
  isFunctionKey,
  isReservedShortcut,
  layerUsage,
  usesOnlyBase,
} from "./editor-model";
import { isHoldAction, normaliseChord } from "./shortcut-keys";
import type { Language } from "./store";
import {
  TEMPLATES,
  type TemplateId,
  templateForDevice,
  templateName,
  templateProfile,
  templateProfileName,
  templateText,
} from "./templates";

const languages: Language[] = ["zh-CN", "en"];
const ids = TEMPLATES.map((template) => template.id);
const bindings = (profile: Profile) =>
  Object.entries(profile.layers.base as Record<string, ProfileBinding>);
const pressKeys = (binding: ProfileBinding | undefined) => {
  const action = binding?.press;
  return action?.type === "keys" ? (action.keys as string) : undefined;
};
// Shortcuts the apps already have; every other key is left for the user.
const fixed = new Set(["Ctrl+Z", "Ctrl+T", "Space", "Ctrl", "Shift", "Alt"]);
// Pads whose chips each template must reach.
const pads: Record<TemplateId, { type: string; buttonMask?: number }[]> = {
  standard: [{ type: "xboxone" }, { type: "ps5" }, { type: "xbox360" }],
  joycon: [
    { type: "joyconleft", buttonMask: 0xf },
    { type: "joyconright", buttonMask: 0xf },
  ],
};
// The emergency pause pair of each pad (daemon/internal/input/combo.go).
const pausePair: Record<TemplateId, [string, string]> = {
  standard: ["back", "start"],
  joycon: ["leftshoulder", "rightshoulder"],
};

describe.each(ids)("template %s", (id) => {
  describe.each(languages)("in %s", (language) => {
    const profile = templateProfile(id, "绘画", language);

    it("is written at the current profile format version", () => {
      expect(profile.schemaVersion).toBe(1);
      expect(JSON.parse(templateText(id, "绘画", language)).schemaVersion).toBe(
        1,
      );
    });

    it("uses only the base layer and press gestures", () => {
      expect(Object.keys(profile)).toEqual([
        "schemaVersion",
        "name",
        "analog",
        "layers",
      ]);
      expect(Object.keys(profile.layers)).toEqual(["base"]);
      expect(profile.common).toBeUndefined();
      expect(usesOnlyBase(layerUsage(profile))).toBe(true);
      expect(layerUsage(profile).profileActions).toBe(0);
      expect(profile.analog).toEqual({ ls: { mode: "fourWay" } });
      for (const [control, binding] of bindings(profile)) {
        expect(binding.label, control).toBeTruthy();
        if (!binding.press) continue;
        expect(Object.keys(binding), control).toEqual(["label", "press"]);
        expect(binding.press.type, control).toBe("keys");
      }
    });

    it("sets only shortcuts that need no setup and names the other keys (G5)", () => {
      const set = bindings(profile).filter(([, binding]) => binding.press);
      const chords = set.map(([, binding]) =>
        normaliseChord(pressKeys(binding) ?? ""),
      );
      expect(new Set(chords).size).toBe(chords.length);
      for (const chord of chords) {
        expect(fixed).toContain(chord);
        expect(isReservedShortcut(chord as string)).toBe(false);
      }
      // The user records the rest; until then the key shows its label.
      const named = bindings(profile).filter(([, binding]) => !binding.press);
      expect(named.length).toBeGreaterThan(0);
      for (const [control, binding] of named)
        expect(Object.keys(binding), control).toEqual(["label"]);
    });

    it("holds only Space and modifiers and repeats nothing", () => {
      for (const [control, binding] of bindings(profile)) {
        if (!binding.press) continue;
        const keys = pressKeys(binding) as string;
        expect(isHoldAction("press", binding.press), control).toBe(
          ["Space", "Ctrl", "Shift", "Alt"].includes(keys),
        );
        expect(binding.press.type === "keys" && binding.press.repeat).toBe(
          undefined,
        );
      }
    });

    it("binds neither View nor Menu, which are function keys (ADR-0069)", () => {
      for (const [control] of bindings(profile))
        expect(isFunctionKey(control), control).toBe(false);
    });

    it("never holds both buttons of the emergency pause", () => {
      const base = profile.layers.base as Record<string, ProfileBinding>;
      const held = pausePair[id].filter((control) =>
        gestures.some((gesture) =>
          isHoldAction(gesture, base[control]?.[gesture]),
        ),
      );
      expect(held.length).toBeLessThan(2);
    });

    it("binds only controls that have a chip", () => {
      for (const pad of pads[id]) {
        const chips = controllerCallouts({ ...pad, buttonLabels: {} }).map(
          (callout) => callout.control,
        );
        for (const [control] of bindings(profile))
          expect(chips, `${pad.type} ${control}`).toContain(
            calloutFor(control),
          );
      }
    });
  });

  it("gives the same keys in either language", () => {
    const keys = (language: Language) =>
      bindings(templateProfile(id, "x", language)).map(([control, binding]) => [
        control,
        pressKeys(binding),
      ]);
    expect(keys("en")).toEqual(keys("zh-CN"));
  });
});

describe("templates", () => {
  it("give an action the same key on either pad", () => {
    const byLabel = (id: TemplateId) =>
      new Map(
        bindings(templateProfile(id, "x", "zh-CN")).map(([, binding]) => [
          binding.label,
          pressKeys(binding),
        ]),
      );
    const standard = byLabel("standard");
    const joycon = byLabel("joycon");
    expect(joycon.size).toBeGreaterThan(8);
    for (const [label, keys] of joycon)
      expect(standard.get(label), label).toBe(keys);
  });

  it("have names in both languages", () => {
    expect(new Set(ids).size).toBe(ids.length);
    expect(templateName("standard", "zh-CN")).toBe("标准手柄");
    expect(templateName("joycon", "zh-CN")).toBe("单只 Joy-Con 横握");
    expect(templateName("joycon", "en")).toBe("Single Joy-Con, sideways");
    expect(templateProfileName("zh-CN")).toBe("绘画");
    expect(templateProfileName("en")).toBe("Painting");
  });

  it("pick the Joy-Con template only for a sideways Joy-Con", () => {
    expect(templateForDevice()).toBe("standard");
    expect(templateForDevice({ type: "xboxone", buttonMask: 0xffff })).toBe(
      "standard",
    );
    expect(templateForDevice({ type: "joyconleft", buttonMask: 0xf })).toBe(
      "joycon",
    );
    expect(templateForDevice({ type: "joyconright", buttonMask: 1 })).toBe(
      "joycon",
    );
    // Upright: no face buttons.
    expect(templateForDevice({ type: "joyconleft", buttonMask: 0x600 })).toBe(
      "standard",
    );
    expect(templateForDevice({ type: "joyconright" })).toBe("standard");
  });

  it("write the name given and end the text with a newline", () => {
    const text = templateText("standard", "我的配置", "zh-CN");
    expect(text.endsWith("}\n")).toBe(true);
    expect(JSON.parse(text)).toEqual(
      templateProfile("standard", "我的配置", "zh-CN"),
    );
    expect(JSON.parse(text).name).toBe("我的配置");
  });
});

// The daemon's profile and engine tests load these files (TestExamples), so
// the Go parser and compiler check every template. Regenerate them with
// PADMATE_UPDATE_TEMPLATES=1 npx vitest run src/renderer/templates.test.ts
const fixtureDir = fileURLToPath(
  new URL("../../../examples/profiles/templates/", import.meta.url),
);
const fixtures = ids.flatMap((id) =>
  languages.map((language) => ({
    id,
    language,
    file: `${id}-${language === "en" ? "en" : "zh"}.json`,
  })),
);

describe("template fixtures", () => {
  if (process.env.PADMATE_UPDATE_TEMPLATES) {
    mkdirSync(fixtureDir, { recursive: true });
    for (const { id, language, file } of fixtures)
      writeFileSync(
        join(fixtureDir, file),
        templateText(id, templateProfileName(language), language),
      );
  }

  it.each(fixtures)("$file matches templateText", ({ id, language, file }) => {
    expect(readFileSync(join(fixtureDir, file), "utf8")).toBe(
      templateText(id, templateProfileName(language), language),
    );
  });

  it("has no other files", () => {
    expect(readdirSync(fixtureDir).sort()).toEqual(
      fixtures.map(({ file }) => file).sort(),
    );
  });
});
