import type {
  Config,
  Profile,
  ProfileAction,
  ProfileStick,
} from "../shared/generated/schema";

// These compile checks fail when schema generation loses required fields,
// nullable values, or the action discriminator. They are never imported by UI.
export const validProfile: Profile = {
  schemaVersion: 1,
  name: "paint",
  layers: { base: {} },
};
export const disabledRule: Config = {
  schemaVersion: 0,
  rules: [{ exe: "paint.exe", profile: null }],
};
export const persistentOwnOutput: Config = {
  schemaVersion: 0,
  output: { sendToOwnWindow: true },
};
export const oldProfileVersion: Profile = {
  // @ts-expect-error The profile format is frozen at version 1 (PRO-01); 0 is no longer valid.
  schemaVersion: 0,
  name: "paint",
  layers: { base: {} },
};
export const keysAction: ProfileAction = { type: "keys", keys: "Ctrl+Z" };

// @ts-expect-error A profile must contain its schema version.
export const missingProfileVersion: Profile = {
  name: "paint",
  layers: { base: {} },
};
// @ts-expect-error A profile must contain its name.
export const missingProfileName: Profile = {
  schemaVersion: 1,
  layers: { base: {} },
};
// @ts-expect-error A profile must contain its layers.
export const missingProfileLayers: Profile = {
  schemaVersion: 1,
  name: "paint",
};
export const missingBase: Profile = {
  schemaVersion: 1,
  name: "paint",
  // @ts-expect-error The base layer is required even with other layers.
  layers: { tools: {} },
};
// @ts-expect-error Keys actions require a chord.
export const missingKeys: ProfileAction = { type: "keys" };
export const mixedAction: ProfileAction = {
  type: "keys",
  keys: "Z",
  // @ts-expect-error A keys action cannot use a field from the layer action.
  layer: "tools",
};
// @ts-expect-error Only the supported action discriminators are valid.
export const unknownAction: ProfileAction = { type: "teleport", keys: "Z" };
export const precisionAction: ProfileAction = {
  type: "mouse",
  op: "precision",
  factor: 0.3,
};
export const jumpAction: ProfileAction = {
  type: "mouse",
  op: "jump",
  x: 0.5,
  y: 0.5,
};
export const hintsAction: ProfileAction = { type: "hud", op: "hints" };
// @ts-expect-error A HUD action names its operation.
export const missingHudOp: ProfileAction = { type: "hud" };
// @ts-expect-error Only hints is a HUD operation.
export const unknownHudOp: ProfileAction = { type: "hud", op: "radial" };
export const hudWithFactor: ProfileAction = {
  type: "hud",
  op: "hints",
  // @ts-expect-error A HUD action has no factor.
  factor: 0.5,
};
// @ts-expect-error A mouse action names its operation.
export const missingMouseOp: ProfileAction = { type: "mouse" };
// @ts-expect-error Only precision and jump are mouse operations.
export const unknownMouseOp: ProfileAction = { type: "mouse", op: "click" };
export const pointerStick: ProfileStick = {
  mode: "mouse",
  mouseSpeed: 1200,
  mouseSmoothingMs: 0,
  curve: "quadratic",
};
// @ts-expect-error The pointer speed is a number.
export const textSpeed: ProfileStick = { mouseSpeed: "fast" };
// @ts-expect-error Config's schema version is required.
export const missingConfigVersion: Config = {};
export const missingRuleTarget: Config = {
  schemaVersion: 0,
  // @ts-expect-error Rule targets must be explicit, including explicit null.
  rules: [{ exe: "paint.exe" }],
};

export function chordForKeys(action: ProfileAction): string | string[] | null {
  if (action.type === "keys") {
    // @ts-expect-error Discrimination removes fields from other action kinds.
    const wrongField = action.name;
    void wrongField;
    return action.keys;
  }
  return null;
}
