import a from "https://example.invalid/a.js";
import b from "//example.invalid/b.js";
import c from "data:text/javascript,export default 1";

console.log(a, b, c);
