const x = 1;
if (x == -0) {
  (globalThis as { result?: unknown }).result = "zero";
}
