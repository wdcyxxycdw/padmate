import type { EditorHandler } from "./library-handlers";
import { validateAppIconPath } from "./security";

/** What Main needs of Electron's NativeImage. */
export interface IconImage {
  isEmpty(): boolean;
  toDataURL(): string;
}

/**
 * Where icons come from: Electron's shell thumbnail of the file, which is
 * large and sharp when Windows has one, and its file icon (32 px, scaled to
 * the display) otherwise. Main passes nativeImage.createThumbnailFromPath
 * and app.getFileIcon.
 */
export interface IconSource {
  thumbnail(path: string, size: number): Promise<IconImage>;
  fileIcon(path: string): Promise<IconImage>;
}

/** The size asked of the thumbnail: the 40 px tile on a 200 % display. */
export const APP_ICON_SIZE = 96;
/** Icons Main keeps for the session; the oldest goes first. */
export const APP_ICON_CACHE = 128;

/**
 * A bound app's icon (UI-42) as a PNG data URL, "" when the file has none
 * or cannot be read. The renderer supplies only the executable's path,
 * checked by validateAppIconPath; no window or window title is read.
 */
export function appIconHandlers(
  source: IconSource,
): Record<string, EditorHandler> {
  const cache = new Map<string, Promise<string>>();
  const read = async (path: string): Promise<string> => {
    for (const load of [
      () => source.thumbnail(path, APP_ICON_SIZE),
      () => source.fileIcon(path),
    ]) {
      try {
        const image = await load();
        if (!image.isEmpty()) return image.toDataURL();
      } catch {
        // Not every file has a thumbnail; the file icon comes next.
      }
    }
    return "";
  };
  return {
    appIcon: {
      count: 1,
      run: (value): Promise<string> => {
        const path = validateAppIconPath(value);
        const key = path.toLowerCase();
        let icon = cache.get(key);
        if (!icon) {
          icon = read(path);
          cache.set(key, icon);
          if (cache.size > APP_ICON_CACHE)
            cache.delete(cache.keys().next().value as string);
        }
        return icon;
      },
    },
  };
}
