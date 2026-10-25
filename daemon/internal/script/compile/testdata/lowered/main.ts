// goja has no async generators or for await; esbuild lowers both.
async function* count(n: number): AsyncGenerator<number> {
  for (let i = 1; i <= n; i++) {
    yield i;
  }
}

async function sum(): Promise<number> {
  let s = 0;
  for await (const x of count(3)) {
    s += x;
  }
  return s;
}

sum().then((s) => {
  (globalThis as { result?: unknown }).result = s;
});
