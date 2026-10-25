import {
  type MouseKey,
  mediaKeyLabel,
  mediaKeyName,
  mouseKeyName,
} from "../shortcut-keys";
import type { Language } from "../store";

const display: Record<string, string> = {
  lctrl: "Ctrl",
  rctrl: "Ctrl",
  control: "Ctrl",
  ctrl: "Ctrl",
  lshift: "Shift",
  rshift: "Shift",
  shift: "Shift",
  lalt: "Alt",
  ralt: "Alt",
  alt: "Alt",
  lwin: "Win",
  rwin: "Win",
  win: "Win",
  up: "↑",
  down: "↓",
  left: "←",
  right: "→",
  minus: "−",
  "-": "−",
  space: "Space",
  escape: "Esc",
  esc: "Esc",
  enter: "Enter",
  return: "Enter",
  backspace: "Backspace",
  delete: "Del",
};

// The mouse buttons and wheel keys (ADR-0062) in Chinese and English.
const mouseDisplay: Record<MouseKey, readonly [zh: string, en: string]> = {
  MouseLeft: ["左键", "LMB"],
  MouseRight: ["右键", "RMB"],
  MouseMiddle: ["中键", "MMB"],
  MouseX1: ["侧键1", "X1"],
  MouseX2: ["侧键2", "X2"],
  WheelUp: ["滚轮↑", "Wheel↑"],
  WheelDown: ["滚轮↓", "Wheel↓"],
  WheelLeft: ["滚轮←", "Wheel←"],
  WheelRight: ["滚轮→", "Wheel→"],
};

/**
 * Splits one KeySpec chord such as "Ctrl+Shift+Z" into keycap labels. The
 * mouse keys read 左键 or LMB, 滚轮↑ or Wheel↑ by language (Chinese when not
 * given), also by their aliases (LButton). The volume and media keys read
 * 音量加 or Volume up, also by their W3C names (AudioVolumeUp).
 */
export function chordKeys(chord: string, language?: Language): string[] {
  const parts = chord
    .split(/\+(?!$)/)
    .map((part) => part.trim())
    .filter(Boolean);
  return parts.map((part) => {
    const plain = part.replace(/^key(?=[a-z]$)/i, "");
    const mouse = mouseKeyName(plain);
    if (mouse) return mouseDisplay[mouse][language === "en" ? 1 : 0];
    const media = mediaKeyName(plain);
    if (media) return mediaKeyLabel(media, language);
    return (
      display[plain.toLowerCase()] ??
      (plain.length === 1 ? plain.toUpperCase() : plain)
    );
  });
}

/** Renders a KeySpec (a chord, or a sequence of chords) as keycaps. */
export function Keycaps({
  keys,
  large = false,
  className = "",
  language,
}: {
  keys: string | string[];
  large?: boolean;
  className?: string;
  language?: Language;
}) {
  const sequence = Array.isArray(keys) ? keys : [keys];
  return (
    <span className={`keycaps${large ? " large" : ""} ${className}`}>
      {sequence.map((chord, index) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: Sequence positions are the identity of each chord.
        <span key={index} className="keycaps-chord">
          {index > 0 && <span className="keycaps-sep">→</span>}
          {chordKeys(chord, language).map((key, keyIndex) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: A chord may repeat the same key label.
            <kbd key={keyIndex} className="kc">
              {key}
            </kbd>
          ))}
        </span>
      ))}
    </span>
  );
}
