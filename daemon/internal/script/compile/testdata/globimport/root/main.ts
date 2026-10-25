export async function load(name: string) {
  return import(`../${name}.ts`);
}
