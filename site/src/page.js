// The landing page of https://padmate.download/, one HTML string per language.
// The copy, the markup and the classes are the final design (Claude Design,
// version 30); nothing on the page loads anything: the stylesheet and the
// pictures are inline and the fonts are the system's.

import { QR_CODES } from "./qr-codes.js";
import { CSS } from "./style.js";

/** The texts of each language. Markup in them is the design's own. */
const COPY = {
  zh: {
    htmlLang: "zh-CN",
    title: "Padmate 下载",
    description: "Padmate 把常用游戏手柄映射成键盘快捷键。下载 Windows 版。",
    other: { lang: "en", hreflang: "en", label: "English" },
    slogan:
      '<span class="nb">让手柄</span><span class="nb">变成<em>生产力</em>。</span>',
    intro:
      'TourBox太贵了？试试用游戏手柄代替！<br>Padmate 把常用游戏手柄映射成键盘快捷键，支持自定义功能键、摇杆旋钮和脚本。适用于 Xbox、PlayStation、Nintendo Switch Pro 和一代、二代 Joy-Con。<br>开发中，有任何意见欢迎在<a href="https://github.com/wdcyxxycdw/padmate/issues">github issue</a>提出！',
    download: "下载 Windows 版",
    github: "Padmate 免费开源，有能力的话可以点个star~",
    stars: "颗星",
  },
  en: {
    htmlLang: "en",
    title: "Download Padmate",
    description:
      "Padmate maps common game controllers to keyboard shortcuts. Download for Windows.",
    other: { lang: "zh", hreflang: "zh-CN", label: "中文" },
    slogan: "Turn a controller into a <em>productivity tool</em>.",
    intro:
      'No need to buy a TourBox. Try your game controller instead.<br>Padmate maps common game controllers to keyboard shortcuts, with a customizable function key, stick knobs and scripts. It works with Xbox, PlayStation, Nintendo Switch Pro and both generations of Joy-Con.<br>Still in development. Feedback is welcome in <a href="https://github.com/wdcyxxycdw/padmate/issues">GitHub issues</a>.',
    download: "Download for Windows",
    github: "Padmate is free and open source. If you like it, give it a star.",
    stars: "stars",
  },
};

const MARK =
  '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M7 7h10c2 0 3 2 4 7l1 4c0 2-2 3-3 1l-3-3H8l-3 3c-1 2-3 1-3-1l1-4c1-5 2-7 4-7ZM7 10v4M5 12h4"></path></svg>';
// The same mark as the tab icon, so the browser asks the site for no favicon.
const FAVICON = `data:image/svg+xml,${encodeURIComponent(
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="9" fill="#eceef2"/><g transform="translate(4 4)" fill="none" stroke="#0f1013" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M7 7h10c2 0 3 2 4 7l1 4c0 2-2 3-3 1l-3-3H8l-3 3c-1 2-3 1-3-1l1-4c1-5 2-7 4-7ZM7 10v4M5 12h4"/></g></svg>',
)}`;
const ARROW_DOWN =
  '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 3v10M7 8l5 5 5-5M5 20h14"></path></svg>';
const GITHUB =
  '<svg width="22" height="22" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M8 0c4.42 0 8 3.58 8 8a8.013 8.013 0 0 1-5.45 7.59c-.4.08-.55-.17-.55-.38 0-.27.01-1.13.01-2.2 0-.75-.25-1.23-.54-1.48 1.78-.2 3.65-.88 3.65-3.95 0-.88-.31-1.59-.82-2.15.08-.2.36-1.02-.08-2.12 0 0-.67-.22-2.2.82-.64-.18-1.32-.27-2-.27-.68 0-1.36.09-2 .27-1.53-1.03-2.2-.82-2.2-.82-.44 1.1-.16 1.92-.08 2.12-.51.56-.82 1.28-.82 2.15 0 3.06 1.86 3.75 3.64 3.95-.23.2-.44.55-.51 1.07-.46.21-1.61.55-2.33-.66-.15-.24-.6-.83-1.23-.82-.67.01-.27.38.01.53.34.19.73.9.82 1.13.16.45.68 1.31 2.69.94 0 .67.01 1.3.01 1.49 0 .21-.15.45-.55.38A7.995 7.995 0 0 1 0 8c0-4.42 3.58-8 8-8Z"></path></svg>';
const ARROW_OUT =
  '<svg class="site-card-go" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M7 17 17 7M8 7h9v9"></path></svg>';
const STAR =
  '<svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M8 .25a.75.75 0 0 1 .673.418l1.882 3.815 4.21.612a.75.75 0 0 1 .416 1.279l-3.046 2.97.719 4.192a.751.751 0 0 1-1.088.791L8 12.347l-3.766 1.98a.75.75 0 0 1-1.088-.79l.72-4.194L.818 6.374a.75.75 0 0 1 .416-1.28l4.21-.611L7.327.668A.75.75 0 0 1 8 .25Z"></path></svg>';
const CUP =
  '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 9h12v6a5 5 0 0 1-5 5H9a5 5 0 0 1-5-5V9ZM16 11h1.5a2.5 2.5 0 0 1 0 5H16M8 3v3M12 3v3"></path></svg>';
// Points down; the stylesheet turns it over while the card is open.
const CHEVRON =
  '<svg class="site-card-go" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m6 9 6 6 6-6"></path></svg>';

/** @param {string} s */
export function escapeHtml(s) {
  return s.replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
}

/**
 * The star count as GitHub writes it on a repository: 91, 1.2k, 12k.
 *
 * @param {number} n
 */
export function formatStars(n) {
  if (n < 1000) return String(n);
  const k = n / 1000;
  return `${k < 10 ? k.toFixed(1).replace(/\.0$/, "") : Math.floor(k)}k`;
}

/** One QR code: the generated SVG, or the design's placeholder until there is one. */
function code(qr) {
  return qr ? qr : "收款码";
}

function donateCard() {
  return `<details class="site-card site-donate">
<summary><span class="site-card-head"><span class="site-cup">${CUP}</span><span>打赏</span>${CHEVRON}</span><span class="site-card-desc">觉得好用的话，可以请作者喝杯咖啡哦~ ^ ^</span><span class="site-card-meta"><span>微信 · 支付宝</span></span></summary>
<div class="site-qr" role="group" aria-label="收款码">
<figure><div class="code">${code(QR_CODES.wechat)}</div><figcaption>微信</figcaption></figure>
<figure><div class="code">${code(QR_CODES.alipay)}</div><figcaption>支付宝</figcaption></figure>
</div>
</details>`;
}

/**
 * The page in one language.
 *
 * @param {{
 *   lang: "zh" | "en",
 *   installer: string,
 *   version: string,
 *   stars: number | null,
 * }} p installer is the file name on the site; version is without its v; a
 *   null stars hides the count
 * @returns {string}
 */
export function renderPage({ lang, installer, version, stars }) {
  const t = COPY[lang];
  const starItem =
    stars === null
      ? ""
      : `<span title="${escapeHtml(`${stars} ${t.stars}`)}">${STAR}<span class="mono">${escapeHtml(formatStars(stars))}</span></span>`;
  return `<!doctype html>
<html lang="${t.htmlLang}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${t.title}</title>
<meta name="description" content="${escapeHtml(t.description)}">
<meta name="color-scheme" content="dark">
<link rel="icon" href="${FAVICON}">
<style>${CSS}</style>
</head>
<body>
<div class="site">
<header class="site-head">
<a class="site-brand" href="?lang=${lang}"><span class="site-mark">${MARK}</span><span>Padmate</span></a>
<a class="site-lang" href="?lang=${t.other.lang}" hreflang="${t.other.hreflang}" lang="${t.other.hreflang}">${t.other.label}</a>
</header>
<main class="site-hero">
<h1 class="site-slogan">${t.slogan}</h1>
<p class="site-intro">${t.intro}</p>
<div class="site-get">
<a class="site-download" href="/${escapeHtml(installer)}">${ARROW_DOWN}<span class="ver">v${escapeHtml(version)}</span>${t.download}</a>
<p class="site-meta" aria-hidden="true"><span><br><br></span></p>
</div>
</main>
<footer class="site-foot${lang === "en" ? " is-single" : ""}">
<a class="site-card" href="https://github.com/wdcyxxycdw/padmate">
<div class="site-card-head">${GITHUB}<span><span class="owner">wdcyxxycdw</span><span class="slash">/</span>padmate</span>${ARROW_OUT}</div>
<p class="site-card-desc">${t.github}</p>
<div class="site-card-meta"><span><i class="dot"></i>Go</span>${starItem}<span>MIT</span></div>
</a>
${lang === "zh" ? donateCard() : ""}
</footer>
</div>
</body>
</html>
`;
}
