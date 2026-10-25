import { double } from "./lib/util";

const n: number = double(21);
(globalThis as { result?: unknown }).result = n;
