declare const require: (path: string) => unknown;

export function load(name: string) {
  return require("../" + name + ".ts");
}
