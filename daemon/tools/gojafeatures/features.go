package main

// feature is one esbuild "supported" feature name and how to decide it for
// goja.
//
// A probe is a script run in a fresh goja Runtime. The feature is supported
// when the script parses, runs without error and leaves the global ok set to
// true; promise jobs are drained before ok is read. A feature without a
// probe has no runtime meaning in padmate's bundle, a classic IIFE script
// with no import or export, and is recorded as false with the reason.
type feature struct {
	name   string
	probe  string
	reason string
}

// features lists every name esbuild v0.28 accepts in BuildOptions.Supported
// (internal/compat/js_table.go, StringToJSFeature). When bumping esbuild,
// compare that map with this table: TestFeatureNames only catches names
// esbuild no longer accepts, not new ones.
var features = []feature{
	{name: "arbitrary-module-namespace-names", reason: "module syntax; the bundle has no import or export"},
	{name: "array-spread", probe: `var a = [1, ...[2, 3]]; var ok = a.length === 3 && a[2] === 3;`},
	{name: "arrow", probe: `var f = (x) => x * 2; var ok = f(2) === 4;`},
	{name: "async-await", probe: `var ok = false; (async function () { await null; ok = true; })();`},
	{name: "async-generator", probe: `var ok = false; (async function* () { yield 1; })().next().then(function (r) { ok = r.value === 1; });`},
	{name: "bigint", probe: `var ok = typeof 1n === "bigint" && 2n ** 64n === 18446744073709551616n;`},
	{name: "class", probe: `class A { m() { return 1; } } class B extends A { m() { return super.m() + 1; } } var ok = new B().m() === 2;`},
	{name: "class-field", probe: `class A { x = 1; } var ok = new A().x === 1;`},
	{name: "class-private-accessor", probe: `class A { get #x() { return 1; } y() { return this.#x; } } var ok = new A().y() === 1;`},
	{name: "class-private-brand-check", probe: `class A { #x; static is(o) { return #x in o; } } var ok = A.is(new A()) && !A.is({});`},
	{name: "class-private-field", probe: `class A { #x = 1; y() { return this.#x; } } var ok = new A().y() === 1;`},
	{name: "class-private-method", probe: `class A { #m() { return 1; } y() { return this.#m(); } } var ok = new A().y() === 1;`},
	{name: "class-private-static-accessor", probe: `class A { static get #x() { return 1; } static y() { return A.#x; } } var ok = A.y() === 1;`},
	{name: "class-private-static-field", probe: `class A { static #x = 1; static y() { return A.#x; } } var ok = A.y() === 1;`},
	{name: "class-private-static-method", probe: `class A { static #m() { return 1; } static y() { return A.#m(); } } var ok = A.y() === 1;`},
	{name: "class-static-blocks", probe: `class A { static x; static { A.x = 1; } } var ok = A.x === 1;`},
	{name: "class-static-field", probe: `class A { static x = 1; } var ok = A.x === 1;`},
	{name: "const-and-let", probe: `const a = 1; let b = 2; { let b = 3; } var fs = []; for (let i = 0; i < 2; i++) fs.push(() => i); var ok = a + b === 3 && fs[0]() === 0;`},
	{name: "decorators", probe: `function d(v) { return v; } class A { @d m() { return 1; } } var ok = new A().m() === 1;`},
	{name: "default-argument", probe: `function f(a, b = a + 1) { return b; } var ok = f(1) === 2;`},
	{name: "destructuring", probe: `var { a, b: [c] } = { a: 1, b: [2] }; var ok = a === 1 && c === 2;`},
	{name: "dynamic-import", reason: "module loading; the bundle is one script, and esbuild inlines bundled import() calls when this is false"},
	{name: "exponent-operator", probe: `var x = 2; x **= 3; var ok = 2 ** 3 === 8 && x === 8;`},
	{name: "export-star-as", reason: "module syntax; the bundle has no import or export"},
	{name: "for-await", probe: `var ok = false; (async function () { for await (const x of [Promise.resolve(1)]) ok = x === 1; })();`},
	{name: "for-of", probe: `var s = 0; for (const x of [1, 2]) s += x; var ok = s === 3;`},
	{name: "from-base64", probe: `var ok = Uint8Array.fromBase64("aGk=").join() === "104,105";`},
	{name: "function-name-configurable", probe: `function f() {} Object.defineProperty(f, "name", { value: "g" }); var ok = f.name === "g";`},
	{name: "function-or-class-property-access", probe: `var a = function () { return 1; }.call(); var b = class { static x = 2; }.x; var ok = a === 1 && b === 2;`},
	{name: "generator", probe: `function* g() { yield 1; yield 2; } var it = g(); var ok = it.next().value === 1 && it.next().value === 2 && it.next().done;`},
	{name: "hashbang", probe: "#!/usr/bin/env padmate\nvar ok = true;"},
	{name: "import-assertions", reason: "module syntax; the bundle has no import or export"},
	{name: "import-attributes", reason: "module syntax; the bundle has no import or export"},
	{name: "import-defer", reason: "module syntax; the bundle has no import or export"},
	{name: "import-meta", reason: "module syntax; the bundle is a classic script, where import.meta does not exist"},
	{name: "import-source", reason: "module syntax; the bundle has no import or export"},
	{name: "inline-script", reason: "escapes </script> for HTML <script> tags; goja does not load HTML, and esbuild turns this off for non-browser platforms anyway"},
	{name: "logical-assignment", probe: `var a = null, b = 0, c = 1; a ??= 1; b ||= 2; c &&= 3; var ok = a === 1 && b === 2 && c === 3;`},
	{name: "nested-rest-binding", probe: `var [a, ...[b, c]] = [1, 2, 3]; var ok = a === 1 && b === 2 && c === 3;`},
	{name: "new-target", probe: `var t; function F() { t = new.target; } new F(); var ok = t === F;`},
	{name: "node-colon-prefix-import", reason: "Node.js module resolution; padmate rejects Node.js built-in modules"},
	{name: "node-colon-prefix-require", reason: "Node.js module resolution; padmate rejects Node.js built-in modules"},
	{name: "nullish-coalescing", probe: `var a = null; var ok = (a ?? 1) === 1 && (0 ?? 1) === 0;`},
	{name: "object-accessors", probe: `var o = { get x() { return 1; }, set x(v) { this.y = v; } }; o.x = 2; var ok = o.x === 1 && o.y === 2;`},
	{name: "object-extensions", probe: `var k = "a", v = 1; var o = { [k]: 1, v, m() { return 2; } }; var ok = o.a === 1 && o.v === 1 && o.m() === 2;`},
	{name: "object-rest-spread", probe: `var { a, ...r } = { a: 1, b: 2 }; var o = { ...r, c: 3 }; var ok = a === 1 && r.b === 2 && !("a" in r) && o.b === 2 && o.c === 3;`},
	{name: "optional-catch-binding", probe: `var ok = false; try { throw 1; } catch { ok = true; }`},
	{name: "optional-chain", probe: `var o = { a: { b: () => 1 } }; var ok = o?.a?.b?.() === 1 && o.x?.y === undefined && o.x?.() === undefined;`},
	{name: "regexp-dot-all-flag", probe: `var ok = /a.b/s.test("a\nb") && !/a.b/.test("a\nb");`},
	{name: "regexp-lookbehind-assertions", probe: `var ok = /(?<=\$)\d+/.exec("$42")[0] === "42" && /(?<!\$)\b\d+/.exec("$4 5")[0] === "5";`},
	{name: "regexp-match-indices", probe: `var ok = /a(b)/d.exec("ab").indices[1][0] === 1;`},
	{name: "regexp-named-capture-groups", probe: `var ok = /(?<y>\d{4})/.exec("2024").groups.y === "2024";`},
	{name: "regexp-set-notation", probe: `var ok = /[\p{L}--[a-z]]/v.test("A") && !/[\p{L}--[a-z]]/v.test("a");`},
	{name: "regexp-sticky-and-unicode-flags", probe: `var r = /a/y; r.lastIndex = 1; var ok = r.test("ba") && /^.$/u.test("\u{1F600}");`},
	{name: "regexp-unicode-property-escapes", probe: `var ok = /\p{Lu}/u.test("A") && !/\p{Lu}/u.test("a");`},
	{name: "rest-argument", probe: `function f(a, ...r) { return r.length; } var ok = f(1, 2, 3) === 2;`},
	{name: "template-literal", probe: "var x = 1; function t(s, v) { return s.raw[0] + v; } var ok = `a${x}b` === \"a1b\" && t`\\n${2}` === \"\\\\n2\";"},
	{name: "top-level-await", reason: "the bundle runs as a classic script, which has no top-level await; esbuild must reject it at build time"},
	{name: "typeof-exotic-object-is-object", probe: `var ok = typeof new Proxy({}, {}) === "object" && typeof new Proxy(function () {}, {}) === "function";`},
	{name: "unicode-escapes", probe: `var \u{61}b = 1; var ok = ab === 1 && "\u{1F600}" === "😀";`},
	{name: "using", probe: `var ok = false; { using x = { [Symbol.dispose]() { ok = true; } }; }`},
}
