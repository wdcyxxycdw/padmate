package compile

// mathModule is deterministic JavaScript bundled into a script. Tick duration
// is supplied by the host so rate accumulation follows the profile's tickHz.
const mathModule = `
const tickMs = globalThis.__padmate.tickMs;
const finite = v => { if (!Number.isFinite(v)) throw new TypeError("expected a finite number"); return v; };
const wrap = a => ((a % 360) + 360) % 360;
export const clamp = (v, lo, hi) => Math.max(lo, Math.min(v, hi));
export const lerp = (a, b, t) => a + (b - a) * t;
export const remap = (v, a0, a1, b0, b1) => { if (a0 === a1) throw new TypeError("remap: empty input range"); return lerp(b0, b1, (v-a0)/(a1-a0)); };
export function deadzone(v, inner, outer=1) { finite(v); finite(inner); finite(outer); if(inner<0 || outer<=inner) throw new TypeError("deadzone: require 0 <= inner < outer"); return Math.sign(v) * clamp((Math.abs(v)-inner)/(outer-inner),0,1); }
export function curve(v, kind) { finite(v); const p = typeof kind === "number" ? kind : {linear:1,quadratic:2,cubic:3}[kind]; if(!Number.isFinite(p)||p<=0)throw new TypeError("curve: invalid exponent"); return Math.sign(v)*Math.pow(Math.abs(v),p); }
export const angle = (x,y) => wrap(Math.atan2(finite(x),finite(y))*180/Math.PI);
function width(n) { if(!Number.isInteger(n)||n<1||n>360)throw new TypeError("sector: invalid sector count"); return 360/n; }
export function sector(a,n,offset=0) { const w=width(n);return Math.floor((wrap(finite(a)-finite(offset))+w/2)/w)%n; }
export class Hysteresis {
  constructor(on,off) { finite(on);finite(off);if(on<off)throw new TypeError("Hysteresis: on must be >= off");this.on=on;this.off=off;this.active=false; }
  update(v) {finite(v);if(this.active ? v<=this.off : v>=this.on)this.active=!this.active;return this.active;}
}
export class RateAccumulator {
  constructor() {this.credit=0;}
  step(hz,dtMs) {finite(hz);finite(dtMs);if(hz<0||dtMs<0)throw new TypeError("RateAccumulator: negative rate or duration");this.credit+=hz*Math.min(dtMs,2*tickMs)/1000;const due=Math.floor(this.credit+1e-12);this.credit-=due;return Math.min(1,due);}
  reset() {this.credit=0;}
}
export class SectorTracker {
  constructor(n,o={}) {this.n=n;this.width=width(n);this.hysteresis=finite(o.hysteresisDeg===undefined?0:o.hysteresisDeg);this.offset=finite(o.offsetDeg===undefined?0:o.offsetDeg);if(this.hysteresis<0||this.hysteresis>this.width/2)throw new TypeError("SectorTracker: invalid hysteresis");this.previous=-1;}
  update(a) {finite(a);if(this.previous>=0){const distance=Math.abs(wrap(a-this.offset-this.previous*this.width+180)-180);if(distance<=this.width/2+this.hysteresis)return this.previous;}return this.previous=sector(a,this.n,this.offset);}
  reset() {this.previous=-1;}
}
`
