// esbuild lowers `using`, but goja has no Symbol.dispose, so the lowered
// code cannot find the disposer and throws.
function open(): number {
  using res = { [Symbol.dispose]() {} };
  return 1;
}

(globalThis as { result?: unknown }).result = open();
