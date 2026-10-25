import { createHash } from "node:crypto";
import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  PadmateAPI,
  ProfileDocument,
  ProfileSaveResult,
  ProfileValidation,
} from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import { createEditorStore, type EditorStore } from "./editor-store";

// Can the save pipeline lose an edit when the daemon is slow (a cold start)?
// The real editor store runs against a fake daemon with the real revision
// rule (a hash of the bytes), random latencies, profile.changed events that
// race the answers, and puts whose answer is lost (the bridge gives up after
// 10 s although the daemon may have written the file). Fake time, seeded.

afterEach(() => {
  vi.useRealTimers();
});

const TIMEOUT_MS = 10000;
const SEEDS = 150;
const base = JSON.stringify({
  schemaVersion: 1,
  name: "p",
  layers: { base: {} },
});
const context = { id: "paint", device: "d", connection: "c" };
const hash = (text: string) => createHash("sha256").update(text).digest("hex");
const sleep = (ms: number) =>
  new Promise<void>((resolve) => setTimeout(resolve, ms));

function rng(seed: number) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface Knobs {
  /** Max ms a put takes on the daemon after the file is written (the reload). */
  reload: number;
  /** Max ms before the profile.changed event reaches the renderer, from the write. */
  event: number;
  /** Max ms of ordinary request latency. */
  latency: number;
  /** Chance that a put's answer is lost; at most two puts per run, so the final flush can succeed. */
  lost?: number;
  /** No profile.changed reaches the renderer (a dropped event). */
  silent?: boolean;
}

function world(seed: number, knobs: Knobs) {
  const random = rng(seed);
  const between = (max: number) => Math.floor(random() * (max + 1));
  let disk = { text: base, revision: hash(base) };
  let store: EditorStore | undefined;
  let lost = 0;
  // Whether the answer of a put is lost, and then whether the file was
  // written first.
  const fate = () => {
    if (!knobs.lost || lost >= 2 || random() >= knobs.lost) return "answered";
    lost++;
    return random() < 0.5 ? "lost after write" : "lost before write";
  };
  const api = {
    getProfile: async (id: string): Promise<ProfileDocument> => {
      await sleep(between(knobs.latency));
      const file = disk;
      await sleep(between(knobs.latency));
      return { id, text: file.text, revision: file.revision, diagnostics: [] };
    },
    listProfiles: async () => [],
    getScriptDescription: async () => ({
      metadata: { runtime: 0, actions: [] },
    }),
    getScriptRegistrations: async () => [],
    listMacros: async () => ({ macros: [], missing: [] }),
    validateProfile: async (_text: string): Promise<ProfileValidation> => {
      await sleep(between(knobs.latency));
      return { valid: true, diagnostics: [] };
    },
    putProfile: async (
      _id: string,
      text: string,
      revision: string,
    ): Promise<ProfileSaveResult> => {
      const outcome = fate();
      await sleep(between(knobs.latency));
      if (outcome === "lost before write") {
        await sleep(TIMEOUT_MS);
        throw { message: "Daemon request timed out" };
      }
      if (revision !== disk.revision)
        throw {
          message: "revision conflict",
          code: -32009,
          data: { revision: disk.revision },
        };
      disk = { text, revision: hash(text) };
      const revisionNow = disk.revision;
      // The event and the response race each other to the renderer.
      if (!knobs.silent)
        setTimeout(() => {
          void store?.getState().profileChanged();
        }, between(knobs.event));
      if (outcome === "lost after write") {
        await sleep(TIMEOUT_MS);
        throw { message: "Daemon request timed out" };
      }
      await sleep(between(knobs.reload));
      return { revision: revisionNow, applied: true };
    },
  };
  store = createEditorStore(api as unknown as PadmateAPI);
  return { random, between, store, disk: () => disk, lost: () => lost };
}

const withKey = (draft: Profile, control: string, keys: string): Profile => ({
  ...draft,
  layers: {
    ...draft.layers,
    base: {
      ...draft.layers.base,
      [control]: { press: { type: "keys", keys } },
    },
  },
});
/** The shortcuts of both knob ticks that a profile has, in a form to compare. */
function ticks(profile: Profile) {
  const out: Record<string, string> = {};
  for (const control of ["rs.cw", "rs.ccw"]) {
    const keys = (
      profile.layers.base as Record<
        string,
        { press?: { keys?: string } } | undefined
      >
    )[control]?.press?.keys;
    if (keys) out[control] = keys;
  }
  return sorted(out);
}
const sorted = (value: Record<string, string>) =>
  JSON.stringify(Object.entries(value).sort());

const advance = (ms: number) => vi.advanceTimersByTimeAsync(ms);

type Churn = undefined | "flicker" | "device" | "connection";

async function scenario(
  seed: number,
  knobs: Knobs,
  edits: number,
  churn: Churn,
) {
  vi.useFakeTimers();
  const w = world(seed, knobs);
  const { store } = w;
  void store.getState().activate(context);
  await advance(2000);
  expect(store.getState().draft).not.toBeNull();
  const expected: Record<string, string> = {};
  const ctx = (patch: Partial<typeof context>) => ({ ...context, ...patch });
  for (let i = 0; i < edits; i++) {
    // The user edits from what the editor shows now (a fresh render). While
    // the editor reads the file (busy) the drawer is gone: no edit is made.
    if (churn && w.random() < 0.5) {
      if (churn === "flicker") {
        void store.getState().activate({ ...ctx({}), id: "" });
        await advance(w.between(300));
        void store.getState().activate(ctx({}));
      } else if (churn === "device")
        void store.getState().activate(ctx({ device: `d${i}` }));
      else void store.getState().activate(ctx({ connection: `c${i}` }));
      await advance(w.between(40));
    }
    const state = store.getState();
    const draft = state.draft as Profile | null;
    const control = i % 2 === 0 ? "rs.cw" : "rs.ccw";
    const keys = `F${13 + i}`;
    if (draft && !state.busy) {
      store.getState().edit(withKey(draft, control, keys));
      expected[control] = keys;
    }
    await advance(w.between(600));
  }
  await advance(4 * TIMEOUT_MS);
  // A failed save is retried by the next flush, as the user would retry it.
  let flushed = false;
  for (let attempt = 0; attempt < 3 && !flushed; attempt++) {
    const flushing = store.getState().flush();
    await advance(6 * TIMEOUT_MS);
    flushed = await flushing;
  }
  await advance(6 * TIMEOUT_MS);
  return { w, store, expected, flushed };
}

const profiles: Knobs[] = [
  { reload: 0, event: 0, latency: 5 },
  { reload: 800, event: 100, latency: 20 },
  { reload: 3000, event: 60, latency: 50 },
  { reload: 300, event: 2000, latency: 30 },
  { reload: 6000, event: 3000, latency: 200 },
  // Answers that are lost: the events come before or after the failure.
  { reload: 300, event: 100, latency: 20, lost: 0.4 },
  { reload: 800, event: 12000, latency: 50, lost: 0.4 },
  { reload: 6000, event: 3000, latency: 200, lost: 0.6 },
];

describe("editor save pipeline with a slow daemon", () => {
  for (const churn of [undefined, "flicker", "device", "connection"] as const) {
    for (const knobs of profiles) {
      const lost = knobs.lost ? `, lost ${knobs.lost}` : "";
      const quiet = churn === undefined || churn === "device";
      it(`keeps every edit (churn ${churn}, reload ${knobs.reload}, event ${knobs.event}${lost})`, async () => {
        const failures: string[] = [];
        let losses = 0;
        for (let seed = 1; seed <= SEEDS; seed++) {
          const { w, store, expected, flushed } = await scenario(
            seed,
            knobs,
            5,
            churn,
          );
          losses += w.lost();
          const want = sorted(expected);
          const state = store.getState();
          const onDisk = ticks(JSON.parse(w.disk().text));
          const shown = ticks(state.draft as Profile);
          // Nobody else writes the file, so it never replaces an edit; only
          // a profile that was left and opened again may say it did.
          const notice = quiet && state.reloaded;
          if (onDisk !== want || shown !== want || !flushed || notice)
            failures.push(
              `seed ${seed}: disk ${onDisk} draft ${shown} want ${want} flushed=${flushed} reloaded=${state.reloaded} discarded=${state.discarded} problem=${state.saveProblem}`,
            );
          vi.useRealTimers();
        }
        expect(failures.slice(0, 5)).toEqual([]);
        // The lost answers did happen.
        if (knobs.lost) expect(losses).toBeGreaterThan(SEEDS / 2);
      });
    }
  }
});

/** Edits, undo and redo in a mix, with nobody else writing the file. */
async function undoScenario(seed: number, knobs: Knobs) {
  vi.useFakeTimers();
  const w = world(seed, knobs);
  const { store } = w;
  void store.getState().activate(context);
  await advance(2000);
  expect(store.getState().draft).not.toBeNull();
  for (let i = 0; i < 8; i++) {
    const state = store.getState();
    const draft = state.draft as Profile | null;
    const pick = w.random();
    if (pick < 0.5) {
      if (draft && !state.busy)
        state.edit(
          withKey(draft, i % 2 === 0 ? "rs.cw" : "rs.ccw", `F${13 + i}`),
        );
    } else if (pick < 0.8) state.undo();
    else state.redo();
    await advance(w.between(600));
  }
  await advance(4 * TIMEOUT_MS);
  let flushed = false;
  for (let attempt = 0; attempt < 3 && !flushed; attempt++) {
    const flushing = store.getState().flush();
    await advance(6 * TIMEOUT_MS);
    flushed = await flushing;
  }
  await advance(6 * TIMEOUT_MS);
  return { w, store, flushed };
}

describe("editor save pipeline with undo and a lost answer", () => {
  const silent: Knobs = {
    reload: 300,
    event: 0,
    latency: 20,
    lost: 0.6,
    silent: true,
  };
  for (const knobs of [...profiles, silent]) {
    const lost = knobs.lost ? `, lost ${knobs.lost}` : "";
    const quiet = knobs.silent ? ", no events" : "";
    it(`ends with the file as the draft (reload ${knobs.reload}, event ${knobs.event}${lost}${quiet})`, async () => {
      const failures: string[] = [];
      for (let seed = 1; seed <= SEEDS; seed++) {
        const { w, store, flushed } = await undoScenario(seed, knobs);
        const state = store.getState();
        const onDisk = ticks(JSON.parse(w.disk().text));
        const shown = ticks(state.draft as Profile);
        if (onDisk !== shown || !flushed || state.reloaded)
          failures.push(
            `seed ${seed}: disk ${onDisk} draft ${shown} flushed=${flushed} reloaded=${state.reloaded} problem=${state.saveProblem}`,
          );
        vi.useRealTimers();
      }
      expect(failures.slice(0, 5)).toEqual([]);
    });
  }
});
