package main

import "strings"

// libComponent is one TypeScript standard library component (a
// lib.<name>.d.ts file) and the members that stand for it in goja.
//
// A probe is one of:
//   - "Base: m1 m2 @@iterator": each member must be in Base ("m" in
//     Object(Base)); @@x means the key Symbol.x. Base is a JS expression
//     such as Array.prototype or globalThis, or a %Name% intrinsic from
//     intrinsics. When Base itself is missing, only Base is reported.
//   - "js:<expr>": the expression must evaluate to true. Used for members
//     that live on instances, and for components that only add overloads.
type libComponent struct {
	name string
	// probes decide the component: it is referenced only if all of them
	// pass.
	probes []string
	// optional probes cover legacy or Annex B members that goja leaves
	// out. Missing ones make the component partial but keep it referenced.
	optional []string
	// note is printed next to the component in the generated comments.
	note string
	// lowered is set for components whose syntax esbuild lowers, since the
	// feature table turns it off in goja. They are referenced even when goja
	// lacks their members, so that tsc accepts the syntax; the text says
	// what scripts must not use at runtime.
	lowered string
	// unreferenced is set for components that are probed and reported but
	// never referenced: TypeScript 5.9 (the version Monaco 0.56 bundles) has
	// no es2025.* libs, and an esnext.* lib changes content between
	// TypeScript versions, so a reference could pull in members goja lacks.
	unreferenced bool
}

// intrinsics are objects that have no global name.
var intrinsics = map[string]string{
	"%GeneratorFunction%":                          `Object.getPrototypeOf(function* () {}).constructor`,
	"%GeneratorFunction.prototype%":                `Object.getPrototypeOf(function* () {})`,
	"%GeneratorFunction.prototype.prototype%":      `Object.getPrototypeOf(function* () {}).prototype`,
	"%AsyncGeneratorFunction%":                     `Object.getPrototypeOf(async function* () {}).constructor`,
	"%AsyncGeneratorFunction.prototype.prototype%": `Object.getPrototypeOf(async function* () {}).prototype`,
	"%IteratorPrototype%":                          `Object.getPrototypeOf(Object.getPrototypeOf([][Symbol.iterator]()))`,
	"%ArrayIteratorPrototype%":                     `Object.getPrototypeOf([][Symbol.iterator]())`,
	"%MapIteratorPrototype%":                       `Object.getPrototypeOf(new Map()[Symbol.iterator]())`,
	"%SetIteratorPrototype%":                       `Object.getPrototypeOf(new Set()[Symbol.iterator]())`,
	"%StringIteratorPrototype%":                    `Object.getPrototypeOf(""[Symbol.iterator]())`,
	"%RegExpStringIteratorPrototype%":              `Object.getPrototypeOf(/a/[Symbol.matchAll](""))`,
}

var (
	typedArrays  = []string{"Int8Array", "Uint8Array", "Uint8ClampedArray", "Int16Array", "Uint16Array", "Int32Array", "Uint32Array", "Float32Array", "Float64Array"}
	bigintArrays = []string{"BigInt64Array", "BigUint64Array"}
)

// each expands format (with %s for the constructor name) for each name.
func each(names []string, format string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strings.ReplaceAll(format, "%s", n)
	}
	return out
}

func concat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

const (
	typedArrayMembers = "BYTES_PER_ELEMENT buffer byteLength byteOffset copyWithin every fill filter find findIndex forEach indexOf join lastIndexOf length map reduce reduceRight reverse set slice some sort subarray toLocaleString toString valueOf"
	intlNote          = "goja has no Intl"
	intlRefNote       = "references es2020.intl, so tsc also loads the es2018.intl and es2020.intl types; goja ignores locales and options"
	asyncLoweredNote  = "referenced anyway, because esbuild lowers async generators and for await; scripts must not use Symbol.asyncIterator or the async generator objects directly"
)

// libComponents lists the TypeScript lib components padmate cares about,
// in reference order. The member lists follow lib.<name>.d.ts in
// TypeScript 5.9/6.0. Host libs (dom, webworker, scripthost) are left out:
// goja has no host APIs.
var libComponents = []libComponent{
	{
		name: "es5",
		probes: concat([]string{
			"globalThis: NaN Infinity Object Function String Boolean Number Math Date RegExp Error EvalError RangeError ReferenceError SyntaxError TypeError URIError JSON Array ArrayBuffer DataView eval parseInt parseFloat isNaN isFinite decodeURI decodeURIComponent encodeURI encodeURIComponent escape unescape",
			"Object.prototype: constructor toString toLocaleString valueOf hasOwnProperty isPrototypeOf propertyIsEnumerable",
			"Object: getPrototypeOf getOwnPropertyDescriptor getOwnPropertyNames create defineProperty defineProperties seal freeze preventExtensions isSealed isFrozen isExtensible keys",
			"Function.prototype: apply call bind toString length arguments caller",
			"js:(function () { return arguments.length === 1 && typeof arguments.callee === 'function'; })(1)",
			"String.prototype: toString charAt charCodeAt concat indexOf lastIndexOf localeCompare match replace search slice split substring toLowerCase toLocaleLowerCase toUpperCase toLocaleUpperCase trim length substr valueOf",
			"String: fromCharCode",
			"Boolean.prototype: valueOf",
			"Number.prototype: toString toFixed toExponential toPrecision valueOf toLocaleString",
			"Number: MAX_VALUE MIN_VALUE NaN NEGATIVE_INFINITY POSITIVE_INFINITY",
			"Math: E LN10 LN2 LOG2E LOG10E PI SQRT1_2 SQRT2 abs acos asin atan atan2 ceil cos exp floor log max min pow random round sin sqrt tan",
			"Date.prototype: toString toDateString toTimeString toLocaleString toLocaleDateString toLocaleTimeString valueOf getTime getFullYear getUTCFullYear getMonth getUTCMonth getDate getUTCDate getDay getUTCDay getHours getUTCHours getMinutes getUTCMinutes getSeconds getUTCSeconds getMilliseconds getUTCMilliseconds getTimezoneOffset setTime setMilliseconds setUTCMilliseconds setSeconds setUTCSeconds setMinutes setUTCMinutes setHours setUTCHours setDate setUTCDate setMonth setUTCMonth setFullYear setUTCFullYear toUTCString toISOString toJSON",
			"Date: parse UTC now",
			"RegExp.prototype: exec test source global ignoreCase multiline compile",
			"js:/a/g.lastIndex === 0 && /b/.exec('ab').index === 1 && /b/.exec('ab').input === 'ab'",
			"Error.prototype: name message",
			"js:typeof new Error('x').stack === 'string'",
			"JSON: parse stringify",
			"Array.prototype: length toString toLocaleString pop push concat join reverse shift slice sort splice unshift indexOf lastIndexOf every some forEach map filter reduce reduceRight",
			"Array: isArray",
			"Promise.prototype: then catch",
			"ArrayBuffer.prototype: byteLength slice",
			"ArrayBuffer: isView",
			"DataView.prototype: buffer byteLength byteOffset getFloat32 getFloat64 getInt8 getInt16 getInt32 getUint8 getUint16 getUint32 setFloat32 setFloat64 setInt8 setInt16 setInt32 setUint8 setUint16 setUint32",
		},
			each(typedArrays, "globalThis: %s"),
			each(typedArrays, "%s.prototype: "+typedArrayMembers),
			each(typedArrays, "%s: BYTES_PER_ELEMENT of from"),
		),
		optional: []string{
			// Legacy RegExp statics, deprecated in TypeScript.
			"RegExp: $1 $2 $3 $4 $5 $6 $7 $8 $9 input $_ lastMatch $& lastParen $+ leftContext $` rightContext $'",
			"Intl: Collator NumberFormat DateTimeFormat",
		},
		note: "declares Intl.Collator, Intl.NumberFormat and Intl.DateTimeFormat; tsc accepts them but goja has no Intl",
	},
	{
		name: "es2015.core",
		probes: concat([]string{
			"Array.prototype: find findIndex fill copyWithin toLocaleString",
			"Array: from of",
			"Function.prototype: name",
			"Math: clz32 imul sign log10 log2 log1p expm1 cosh sinh tanh acosh asinh atanh hypot trunc fround cbrt",
			"Number: EPSILON isFinite isInteger isNaN isSafeInteger MAX_SAFE_INTEGER MIN_SAFE_INTEGER parseFloat parseInt",
			"Object: assign getOwnPropertySymbols keys is setPrototypeOf",
			"RegExp.prototype: flags sticky unicode",
			"String.prototype: codePointAt includes endsWith normalize repeat startsWith",
			"String: fromCodePoint raw",
		}, each(typedArrays, "%s.prototype: toLocaleString")),
		optional: []string{
			// Annex B HTML methods.
			"String.prototype: anchor big blink bold fixed fontcolor fontsize italics link small strike sub sup",
		},
	},
	{
		name: "es2015.collection",
		probes: []string{
			"globalThis: Map WeakMap Set WeakSet",
			"Map.prototype: clear delete forEach get has set size",
			"WeakMap.prototype: delete get has set",
			"Set.prototype: add clear delete forEach has size",
			"WeakSet.prototype: add delete has",
		},
	},
	{
		name: "es2015.generator",
		probes: []string{
			"%GeneratorFunction.prototype.prototype%: next return throw @@iterator",
			"%GeneratorFunction%: length name prototype",
			"%GeneratorFunction.prototype%: prototype",
		},
	},
	{
		name: "es2015.iterable",
		probes: concat([]string{
			"Symbol: iterator",
			"%IteratorPrototype%: @@iterator",
			"%ArrayIteratorPrototype%: next @@iterator",
			"Array.prototype: @@iterator entries keys values",
			"Array: from",
			"js:typeof (function () { return arguments; })()[Symbol.iterator] === 'function'",
			"%MapIteratorPrototype%: next @@iterator",
			"Map.prototype: @@iterator entries keys values",
			"%SetIteratorPrototype%: next @@iterator",
			"Set.prototype: @@iterator entries keys values",
			"Promise: all race",
			"%StringIteratorPrototype%: next @@iterator",
			"String.prototype: @@iterator",
		},
			each(typedArrays, "%s.prototype: @@iterator entries keys values"),
			each(typedArrays, "%s: from"),
		),
	},
	{
		name: "es2015.promise",
		probes: []string{
			"globalThis: Promise",
			"Promise: all race reject resolve",
		},
	},
	{
		name: "es2015.proxy",
		probes: []string{
			"globalThis: Proxy",
			"Proxy: revocable",
		},
	},
	{
		name: "es2015.reflect",
		probes: []string{
			"globalThis: Reflect",
			"Reflect: apply construct defineProperty deleteProperty get getOwnPropertyDescriptor getPrototypeOf has isExtensible ownKeys preventExtensions set setPrototypeOf",
		},
	},
	{
		name: "es2015.symbol",
		probes: []string{
			"globalThis: Symbol",
			"Symbol: for keyFor",
			"Symbol.prototype: toString valueOf",
		},
	},
	{
		name: "es2015.symbol.wellknown",
		probes: concat([]string{
			"Symbol: hasInstance isConcatSpreadable match replace search species split toPrimitive toStringTag unscopables",
			"Symbol.prototype: @@toPrimitive @@toStringTag",
			"Array.prototype: @@unscopables",
			"Date.prototype: @@toPrimitive",
			"Map.prototype: @@toStringTag",
			"WeakMap.prototype: @@toStringTag",
			"Set.prototype: @@toStringTag",
			"WeakSet.prototype: @@toStringTag",
			"JSON: @@toStringTag",
			"Function.prototype: @@hasInstance",
			"%GeneratorFunction.prototype%: @@toStringTag",
			"Math: @@toStringTag",
			"Promise.prototype: @@toStringTag",
			"Promise: @@species",
			"RegExp.prototype: @@match @@replace @@search @@split",
			"RegExp: @@species",
			"ArrayBuffer.prototype: @@toStringTag",
			"DataView.prototype: @@toStringTag",
			"Array: @@species",
			"Map: @@species",
			"Set: @@species",
			"ArrayBuffer: @@species",
		}, each(typedArrays, "%s.prototype: @@toStringTag")),
	},
	{
		name:   "es2016.array.include",
		probes: concat([]string{"Array.prototype: includes"}, each(typedArrays, "%s.prototype: includes")),
	},
	{name: "es2016.intl", probes: []string{"globalThis: Intl"}, note: intlNote},
	{
		name:   "es2017.arraybuffer",
		probes: []string{"js:new ArrayBuffer(8).byteLength === 8"},
		note:   "types only (an ArrayBuffer constructor overload)",
	},
	{
		name:   "es2017.date",
		probes: []string{"js:Date.UTC(2020) === 1577836800000"},
		note:   "types only (a Date.UTC overload)",
	},
	{name: "es2017.intl", probes: []string{"globalThis: Intl"}, note: intlNote},
	{
		name:   "es2017.object",
		probes: []string{"Object: values entries getOwnPropertyDescriptors"},
	},
	{
		name: "es2017.sharedmemory",
		probes: []string{
			"globalThis: SharedArrayBuffer Atomics",
			"SharedArrayBuffer.prototype: byteLength slice @@toStringTag",
			"Atomics: add and compareExchange exchange isLockFree load or store sub wait notify xor @@toStringTag",
		},
	},
	{
		name:   "es2017.string",
		probes: []string{"String.prototype: padStart padEnd"},
	},
	{
		name:   "es2017.typedarrays",
		probes: each(typedArrays, "js:new %s().length === 0"),
		note:   "types only (typed array constructor overloads)",
	},
	{
		name: "es2018.asyncgenerator",
		probes: []string{
			"%AsyncGeneratorFunction.prototype.prototype%: next return throw @@asyncIterator",
			"%AsyncGeneratorFunction%: length name",
		},
		lowered: asyncLoweredNote,
	},
	{
		name:    "es2018.asynciterable",
		probes:  []string{"Symbol: asyncIterator"},
		lowered: asyncLoweredNote,
	},
	{name: "es2018.intl", probes: []string{"globalThis: Intl"}, note: intlNote},
	{
		name:   "es2018.promise",
		probes: []string{"Promise.prototype: finally"},
	},
	{
		name: "es2018.regexp",
		probes: []string{
			"RegExp.prototype: dotAll",
			"js:/(?<a>x)/.exec('x').groups.a === 'x'",
		},
	},
	{
		name:   "es2019.array",
		probes: []string{"Array.prototype: flatMap flat"},
	},
	{name: "es2019.intl", probes: []string{"globalThis: Intl"}, note: intlNote},
	{
		name:   "es2019.object",
		probes: []string{"Object: fromEntries"},
	},
	{
		name:   "es2019.string",
		probes: []string{"String.prototype: trimEnd trimStart trimLeft trimRight"},
	},
	{
		name:   "es2019.symbol",
		probes: []string{"Symbol.prototype: description"},
	},
	{
		name: "es2020.bigint",
		probes: concat([]string{
			"globalThis: BigInt BigInt64Array BigUint64Array",
			"BigInt.prototype: toString toLocaleString valueOf @@toStringTag",
			"BigInt: asIntN asUintN",
			"DataView.prototype: getBigInt64 getBigUint64 setBigInt64 setBigUint64",
		},
			each(bigintArrays, "%s.prototype: "+typedArrayMembers+" entries includes keys values @@iterator @@toStringTag"),
			each(bigintArrays, "%s: BYTES_PER_ELEMENT of from"),
		),
		note: intlRefNote,
	},
	{
		name:   "es2020.date",
		probes: []string{"Date.prototype: toLocaleString toLocaleDateString toLocaleTimeString"},
		note:   intlRefNote,
	},
	{name: "es2020.intl", probes: []string{"globalThis: Intl"}, note: intlNote},
	{
		name:   "es2020.number",
		probes: []string{"Number.prototype: toLocaleString"},
		note:   intlRefNote,
	},
	{
		name:   "es2020.promise",
		probes: []string{"Promise: allSettled"},
	},
	{
		name:   "es2020.sharedmemory",
		probes: []string{"globalThis: Atomics"},
	},
	{
		name:   "es2020.string",
		probes: []string{"String.prototype: matchAll toLocaleLowerCase toLocaleUpperCase localeCompare"},
		note:   intlRefNote,
	},
	{
		name: "es2020.symbol.wellknown",
		probes: []string{
			"Symbol: matchAll",
			"RegExp.prototype: @@matchAll",
			"%RegExpStringIteratorPrototype%: next @@iterator",
		},
	},
	{name: "es2021.intl", probes: []string{"globalThis: Intl"}, note: intlNote},
	{
		name: "es2021.promise",
		probes: []string{
			"globalThis: AggregateError",
			"Promise: any",
			"js:new AggregateError([1]).errors[0] === 1",
		},
	},
	{
		name:   "es2021.string",
		probes: []string{"String.prototype: replaceAll"},
	},
	{
		name: "es2021.weakref",
		probes: []string{
			"globalThis: WeakRef FinalizationRegistry",
			"WeakRef.prototype: deref @@toStringTag",
			"FinalizationRegistry.prototype: register unregister @@toStringTag",
		},
	},
	{
		name: "es2022.array",
		probes: concat([]string{"Array.prototype: at"},
			each(typedArrays, "%s.prototype: at"),
			each(bigintArrays, "%s.prototype: at"),
		),
	},
	{
		name:   "es2022.error",
		probes: []string{"js:new Error('', { cause: 1 }).cause === 1"},
	},
	{name: "es2022.intl", probes: []string{"globalThis: Intl"}, note: intlNote},
	{
		name:   "es2022.object",
		probes: []string{"Object: hasOwn"},
	},
	{
		name: "es2022.regexp",
		probes: []string{
			"RegExp.prototype: hasIndices",
			"js:/a/d.exec('a').indices[0][0] === 0",
		},
	},
	{
		name:   "es2022.string",
		probes: []string{"String.prototype: at"},
	},
	{
		name: "es2023.array",
		probes: concat([]string{"Array.prototype: findLast findLastIndex toReversed toSorted toSpliced with"},
			each(typedArrays, "%s.prototype: findLast findLastIndex toReversed toSorted with"),
			each(bigintArrays, "%s.prototype: findLast findLastIndex toReversed toSorted with"),
		),
	},
	{
		name: "es2023.collection",
		probes: []string{
			"js:(function () { var s = Symbol('k'); return new WeakMap().set(s, 1).get(s) === 1; })()",
		},
		note: "symbols as WeakMap and WeakSet keys",
	},
	{name: "es2023.intl", probes: []string{"globalThis: Intl"}, note: intlNote},
	{
		name:   "es2024.arraybuffer",
		probes: []string{"ArrayBuffer.prototype: maxByteLength resizable resize detached transfer transferToFixedLength"},
	},
	{
		name:   "es2024.collection",
		probes: []string{"Map: groupBy"},
	},
	{
		name:   "es2024.object",
		probes: []string{"Object: groupBy"},
	},
	{
		name:   "es2024.promise",
		probes: []string{"Promise: withResolvers"},
	},
	{
		name:   "es2024.regexp",
		probes: []string{"RegExp.prototype: unicodeSets"},
	},
	{
		name: "es2024.sharedmemory",
		probes: []string{
			"globalThis: SharedArrayBuffer",
			"Atomics: waitAsync",
		},
	},
	{
		name:   "es2024.string",
		probes: []string{"String.prototype: isWellFormed toWellFormed"},
	},

	// Newer components: es2025.* exist from TypeScript 6.0 on; esnext.* are
	// listed with their TypeScript 6.0 content.
	{
		name:         "es2025.collection",
		probes:       []string{"Set.prototype: union intersection difference symmetricDifference isSubsetOf isSupersetOf isDisjointFrom"},
		note:         "TypeScript 5.9 has these in esnext.collection",
		unreferenced: true,
	},
	{
		name: "es2025.float16",
		probes: []string{
			"globalThis: Float16Array",
			"Math: f16round",
			"DataView.prototype: getFloat16 setFloat16",
		},
		unreferenced: true,
	},
	{name: "es2025.iterator", probes: []string{"globalThis: Iterator"}, unreferenced: true},
	{name: "es2025.promise", probes: []string{"Promise: try"}, unreferenced: true},
	{name: "es2025.regexp", probes: []string{"RegExp: escape"}, unreferenced: true},
	{name: "esnext.array", probes: []string{"Array: fromAsync"}, unreferenced: true},
	{
		name: "esnext.collection",
		probes: []string{
			"Map.prototype: getOrInsert getOrInsertComputed",
			"WeakMap.prototype: getOrInsert getOrInsertComputed",
		},
		unreferenced: true,
	},
	{
		name: "esnext.disposable",
		probes: []string{
			"globalThis: DisposableStack AsyncDisposableStack SuppressedError",
			"Symbol: dispose asyncDispose",
		},
		unreferenced: true,
	},
	{name: "esnext.error", probes: []string{"Error: isError"}, unreferenced: true},
	{
		name: "esnext.typedarrays",
		probes: []string{
			"Uint8Array.prototype: toBase64 setFromBase64 toHex setFromHex",
			"Uint8Array: fromBase64 fromHex",
		},
		unreferenced: true,
	},
}
