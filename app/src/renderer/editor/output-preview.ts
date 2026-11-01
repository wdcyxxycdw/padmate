export function displayChord(chord: string): string {
  return chord
    .split("+")
    .map((key) => key.replace(/^L(Ctrl|Shift|Alt|Win)$/, "$1"))
    .join("+");
}
