import {
  defaultOverlayPrefs,
  type OverlayPrefs,
  parseOverlayPrefs,
} from "../shared/overlay";

// What the UI chose about the overlay (HUD-05, ADR-0063). The choices live in
// the UI's localStorage, like its language, and the UI pushes them here at
// start and whenever one changes. Main keeps the latest valid ones, so an
// overlay made later (it is made after the main window is ready, and again
// after a crash) starts with them, and hands each push to the parts that act
// on it. Until the first push the defaults apply.

/** Whatever acts on a push: the toaster and the overlay window. */
export interface PrefsTarget {
  configure(prefs: Partial<OverlayPrefs>): void;
}

export class OverlayPreferences {
  private current: OverlayPrefs = { ...defaultOverlayPrefs };

  /**
   * The targets are asked each time, as the overlay does not exist before the
   * main window is ready and may be gone after it.
   */
  constructor(private readonly targets: () => Array<PrefsTarget | undefined>) {}

  /** What applies now. */
  get value(): OverlayPrefs {
    return { ...this.current };
  }

  /**
   * Takes a push from the main window's page. Fields that are not valid are
   * ignored, as is a value that is not an object; the valid ones apply, also
   * when others are not. Nothing here throws: the page is told nothing of
   * what was ignored.
   */
  push(raw: unknown): void {
    const prefs = parseOverlayPrefs(raw);
    this.current = { ...this.current, ...prefs };
    for (const target of this.targets()) {
      try {
        target?.configure(prefs);
      } catch {
        // One part failing must not keep the others from the push.
      }
    }
  }
}
