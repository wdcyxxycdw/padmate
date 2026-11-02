// The one stylesheet of the page, inline in a <style> element whose hash the
// Content-Security-Policy names. It is the design's site.css (tokens of
// app/src/renderer/theme.css) with the design tool's inline style attributes
// moved into classes, since the policy allows no style attribute, and the
// donate card's open state keyed on <details open> instead of a class.

export const CSS = `:root {
  color-scheme: dark;
  --bg: #0f1013;
  --line: #1c1f25;
  --surface: #15171c;
  --border: #2a2e36;
  --text: #eceef2;
  --text-2: #a6acb7;
  --text-3: #999fab;
  --text-4: #8c929e;
  --accent: #4cc2ff;
  --accent-ink: #04121c;
  --accent-hover: #6dceff;
  --surface-2: #1b1e24;
  --surface-3: #262a31;
  --amber: #f2b35b;
  --amber-bg: rgba(242, 179, 91, 0.1);
  --shadow-pop: 0 16px 40px rgba(0, 0, 0, 0.5);
  --radius: 10px;
  --font-sans: "Segoe UI Variable Text", "Segoe UI", "Microsoft YaHei UI", "PingFang SC", sans-serif;
  --font-mono: "Cascadia Mono", Consolas, ui-monospace, monospace;
}
body { margin: 0; background: var(--bg); }

.site, .site * { box-sizing: border-box; }
.site { min-height: 100vh; display: flex; flex-direction: column; background: var(--bg); color: var(--text); font: 15px/1.6 var(--font-sans); -webkit-font-smoothing: antialiased; }
.site a { color: inherit; text-decoration: none; }
.site a:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; border-radius: 6px; }

.site-head { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 20px max(24px, env(safe-area-inset-left, 0px)) 0; }
.site-brand { display: inline-flex; align-items: center; gap: 10px; font-size: 15px; font-weight: 600; }
.site-mark { width: 32px; height: 32px; border-radius: 9px; display: grid; place-items: center; background: var(--text); color: var(--bg); }
.site-lang { padding: 6px 10px; border-radius: 8px; color: var(--text-3); font-size: 13px; }
.site-lang:hover { color: var(--text); background: var(--surface); }

.site-hero { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 22px; padding: 48px 24px 64px; text-align: center; }
.site-slogan { margin: 0; max-width: 18em; font-size: clamp(36px, 5.6vw, 76px); font-weight: 700; line-height: 1.12; letter-spacing: -0.015em; text-wrap: balance; }
.site-slogan em { font-style: normal; color: var(--accent); }
.site-intro { margin: 0; max-width: 36em; color: var(--text-2); font-size: 16px; line-height: 1.75; text-wrap: pretty; }
.site .site-intro a { color: var(--text-2); text-decoration: underline; text-decoration-thickness: 1px; text-decoration-color: rgba(166, 172, 183, 0.45); text-underline-offset: 3px; }
.site .site-intro a:hover { color: var(--text); text-decoration-color: currentColor; }

.site-get { margin-top: 18px; display: flex; flex-direction: column; align-items: center; gap: 12px; }
.site-download { display: inline-flex; align-items: center; justify-content: center; gap: 10px; min-height: 52px; padding: 0 28px; border-radius: var(--radius); background: var(--accent); color: var(--accent-ink); font-size: 16px; font-weight: 600; box-shadow: 0 0 0 6px rgba(76, 194, 255, 0.12); }
.site-download:hover { background: var(--accent-hover); }
.site .site-download { color: var(--accent-ink); }
.site-download .ver { font-family: var(--font-mono); font-size: 12.5px; font-weight: 400; color: var(--bg); }
/* An empty spacer of two lines under the button, where the design once had size and SHA-256. */
.site-meta { margin: 0; display: flex; flex-wrap: wrap; justify-content: center; gap: 4px 10px; color: var(--text-3); font-size: 13px; font-variant-numeric: tabular-nums; }

.site-slogan .nb { display: inline-block; }

.site-foot { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; width: 100%; max-width: 760px; margin: 0 auto; padding: 0 24px 40px; }
.site-card { position: relative; display: flex; flex-direction: column; gap: 10px; min-height: 132px; padding: 18px 20px; border: 1px solid var(--border); border-radius: 12px; background: var(--surface); color: var(--text-2); font-size: 13.5px; line-height: 1.55; text-align: left; transition: border-color 0.12s, background 0.12s; }
.site-foot.is-single { grid-template-columns: minmax(0, 348px); justify-content: center; }
.site a.site-card { color: var(--text-2); }
.site-card:hover { border-color: #3a3f49; background: var(--surface-2); }
.site-card-head { display: flex; align-items: center; gap: 10px; color: var(--text); font-size: 15px; font-weight: 600; }
.site-card-head .owner { color: var(--text-3); font-weight: 400; }
.site-card-head .slash { margin: 0 2px; color: var(--text-4); font-weight: 400; }
.site-card-go { margin-left: auto; color: var(--text-4); }
.site-card:hover .site-card-go { color: var(--text-2); }
.site-card-desc { margin: 0; }
.site-card-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 16px; margin-top: auto; color: var(--text-3); font-size: 12.5px; font-variant-numeric: tabular-nums; }
.site-card-meta span { display: inline-flex; align-items: center; gap: 6px; }
.site-card-meta .mono { font-family: var(--font-mono); font-size: 12px; }
.site-card-meta .dot { width: 10px; height: 10px; border-radius: 50%; background: #00add8; }
.site-cup { width: 28px; height: 28px; border-radius: 8px; display: grid; place-items: center; background: var(--amber-bg); color: var(--amber); }

/* Donate card (Chinese page): a <details> that opens in place to show the QR
   codes. Its <summary> is the head row, the text and the meta line, so the
   whole card is the control, as in the design. */
.site-donate { gap: 0; }
.site-donate > summary { display: flex; flex-direction: column; gap: 10px; flex: 1; list-style: none; cursor: pointer; outline: none; }
.site-donate > summary::-webkit-details-marker { display: none; }
.site-donate > summary > span { display: block; }
.site-donate > summary > .site-card-head, .site-donate > summary > .site-card-meta { display: flex; }
.site-donate:has(> summary:focus-visible) { outline: 2px solid var(--accent); outline-offset: 3px; }
.site-donate[open] { border-color: #3a3f49; background: var(--surface-2); }
.site-donate[open] > summary { flex: none; }
.site-donate .site-qr { margin-top: 14px; }
.site-donate[open] .site-card-meta { display: none; }
.site-donate[open] .site-card-go { transform: rotate(180deg); }
.site-foot:has(.site-donate[open]) { align-items: start; }

.site-qr { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-top: 4px; padding-top: 14px; border-top: 1px solid var(--border); }
.site-qr figure { margin: 0; display: flex; flex-direction: column; align-items: center; gap: 8px; }
.site-qr .code { width: 100%; max-width: 160px; aspect-ratio: 1; border-radius: 8px; background: var(--surface-3); display: grid; place-items: center; overflow: hidden; color: var(--text-4); font-size: 12px; }
.site-qr .code > svg, .site-qr .code > img { display: block; width: 100%; height: 100%; }
.site-qr figcaption { color: var(--text-2); }

@media (max-width: 600px) {
  .site-head { padding-top: 16px; }
  .site-hero { gap: 18px; padding: 40px 20px 40px; }
  .site-slogan { font-size: 40px; }
  .site-intro { font-size: 15px; }
  .site-get { align-self: stretch; padding: 0 6px; }
  .site-download { width: 100%; }
  .site-foot, .site-foot.is-single { grid-template-columns: 1fr; gap: 12px; padding: 0 20px 28px; }
  .site-card { min-height: 0; }
}
`;
