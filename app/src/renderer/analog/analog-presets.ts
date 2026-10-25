import type { Profile, ProfileBinding } from "../../shared/generated/schema";
import { baseBinding, usedGestures } from "../editor-model";
import {
  type BindingControl,
  type DirectionGroup,
  directionControls,
  setAnalogSettings,
  setControlBinding,
} from "./analog-editor";

export type AnalogPreset = "arrows" | "wasd";

/** Keys each preset sends for up, right, down and left. */
export const presetKeys: Record<AnalogPreset, readonly string[]> = {
  arrows: ["Up", "Right", "Down", "Left"],
  // Physical positions, so the preset works on any keyboard layout.
  wasd: ["KeyW", "KeyD", "KeyS", "KeyA"],
};

export interface PresetChange {
  control: BindingControl;
  binding: ProfileBinding;
}

/**
 * Cardinal bindings of the edited key set that a preset would replace: any
 * with a gesture or a block (ADR-0045 asks before a block is replaced). A
 * display name alone is kept by the preset, so it is not a change.
 */
export function presetChanges(
  profile: Profile,
  group: DirectionGroup,
): PresetChange[] {
  return directionControls(group, "standard")
    .map((control) => ({ control, binding: baseBinding(profile, control) }))
    .filter(
      ({ binding }) => usedGestures(binding).length > 0 || !!binding.block,
    );
}

/** The direction mode a preset switches to. */
export function presetMode(group: DirectionGroup) {
  return group === "dpad" ? "buttons" : "standard";
}

// Only the four cardinal bindings of the edited key set are replaced; their
// labels stay. Analog settings are profile-wide, so only the mode changes.
export function applyAnalogPreset(
  profile: Profile,
  group: DirectionGroup,
  preset: AnalogPreset,
): Profile {
  let next = setAnalogSettings(profile, group, {
    ...profile.analog?.[group],
    mode: presetMode(group),
  });
  const keys = presetKeys[preset];
  directionControls(group, "standard").forEach((control, index) => {
    const label = baseBinding(profile, control).label;
    next = setControlBinding(next, control, {
      ...(label === undefined ? {} : { label }),
      press: { type: "keys", keys: keys[index] ?? "Up", mode: "hold" },
    });
  });
  return next;
}
