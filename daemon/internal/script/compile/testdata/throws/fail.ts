export function fail(n: number): never {
  throw new Error(`failed with ${n}`);
}
