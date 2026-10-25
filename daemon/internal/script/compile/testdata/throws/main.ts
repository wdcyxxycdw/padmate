import { fail } from "./fail";

type Box = { value: number };

const box: Box = { value: 1 };
fail(box.value);
