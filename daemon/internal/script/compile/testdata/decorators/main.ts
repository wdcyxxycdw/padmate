// goja has no decorators; esbuild lowers them (standard decorators, since
// the fixed tsconfig does not set experimentalDecorators).
function double(target: (x: number) => number, _ctx: unknown) {
  return function (this: unknown, x: number): number {
    return target.call(this, x) * 2;
  };
}

class Calc {
  @double
  add1(x: number): number {
    return x + 1;
  }
}

(globalThis as { result?: unknown }).result = new Calc().add1(2);
