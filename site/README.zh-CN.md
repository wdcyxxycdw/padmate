# padmate.download

[English](README.md)

<https://padmate.download/> 的 Cloudflare Worker `padmate-download-root`。这个地址是 R2 桶 `padmate` 的自定义域名，桶里放安装包、它们的 `.sha256` 和 `latest.yml`（已安装的程序读它来更新）。Worker 加上一个下载页和一个长期有效的直链。决策见 [ADR-0018](../docs/adr/0018-installer-and-updates.zh-CN.md)，发布流程见 [ARCHITECTURE §15.5](../docs/ARCHITECTURE.zh-CN.md)。

## 它做什么

| 请求 | 回答 |
|---|---|
| 浏览器（`Accept` 含 `text/html`）访问 `/` | 下载页，中文或英文。按钮直接指向带版本号的安装包。 |
| 其他请求访问 `/`（curl、wget、PowerShell、脚本、`task release:r2` 的读回） | `302` 到最新安装包，`Cache-Control: no-cache`。 |
| `/latest` | 永远是同一个 `302`：长期有效的直链。 |
| 以上两者，当 `latest.yml` 不可用 | `503`，纯文本。 |
| `HEAD` | 同样的回答，没有正文。其他方法得到 `405`。 |

其他路径都不归 Worker。`/latest.yml`、安装包和 `.sha256` 由 R2 原样提供，更新不经过 Worker。

**为什么看 `Accept`。** 页面出现之前，`/` 是唯一能打开最新安装包的地址。旧帖子里的链接、脚本和 `irm padmate.download/ -OutFile` 仍依赖它。浏览器的 `Accept` 一定列出 `text/html`，这些工具发的是 `*/*` 或什么都不发。所以只有浏览器得到页面。`/` 的回答带 `Vary: Accept`，页面还带 `Accept-Language`。

**最新安装包**是 `/latest.yml` 里 `path:` 一行写的文件。这一行必须是 `padmate-vX.Y.Z[-pre]-windows-amd64-setup.exe`，否则按没有答案处理。`latest.yml` 经 Cache API 读取并缓存 60 秒，所以新版本一分钟内到达页面。跳转是 `302`，不是 `301`，免得浏览器记住旧安装包。跳转永远指向 `https://padmate.download/...`，用 http 来的访客也一样。

**语言。** `?lang=zh` 或 `?lang=en` 优先。否则按 `Accept-Language` 里权重最高的语言：`zh*` 是中文，其余英文。

**GitHub 卡片**显示仓库的星数。Worker 从 GitHub API 取，缓存一小时（失败缓存十分钟）；GitHub 没给数时隐藏。只有中文页有打赏卡片：一个 `<details>`，展开后是微信和支付宝的二维码。

**页面自成一体。** 标记、样式、图标和二维码都内联，浏览器不向其他主机请求任何东西。Content-Security-Policy 不允许脚本，唯一的 `<style>` 只按哈希放行（哈希由 `src/style.js` 在运行时算出，不用手动更新），图片只允许 `data:`。页面还发 `nosniff`、`Referrer-Policy` 和 `Cache-Control: public, max-age=60`。

## 文件

| 路径 | 内容 |
|---|---|
| `src/index.js` | 处理函数：路由、`Accept` 规则、读 `latest.yml` 和 GitHub 及其缓存、响应头。 |
| `src/page.js` | 标记和两种语言的文案。 |
| `src/style.js` | 样式表。 |
| `src/qr-codes.js` | 内联 SVG 二维码。由 `task site:qr` 生成，不要手改。 |
| `qr/payloads.json` | 二维码编码的内容，由 `task site:qr` 写入。 |
| `tools/` | `qr.mjs`（二维码生成器）、`crop-image.mjs`（把不是 QR 码的码从图片里裁出来）和 `check-qr.mjs`（二维码还是占位时让 `site:deploy` 拒绝）。 |
| `test/` | 测试（`node:test`，无依赖）。 |
| `wrangler.toml` | Worker 的名字、入口和路由。 |

## 任务

在仓库任意目录都能运行。

| 任务 | 作用 |
|---|---|
| `task site:install` | 安装 `site/` 固定版本的 npm 包。只有 `site:qr` 需要。 |
| `task site:test` | 测试。属于 `task ci`。 |
| `task site:dev` | `wrangler dev`，本地运行。读线上真实的 `latest.yml` 和 GitHub API，不上传任何东西。 |
| `task site:dry-run` | `wrangler deploy --dry-run`：打包并检查到 `.cache/site-dry-run`，不上传。 |
| `task site:qr -- --wechat <图片> --alipay <图片>` | 更新二维码（见下）。 |
| `task site:deploy` | 检查二维码、运行测试，再 `npx wrangler@4.151.1 deploy`。写线上的 Worker。 |

## 更新二维码

1. `task site:install`（一次）。
2. 微信赞赏码是圆形的小程序码，不是 QR 码，没法重画。从 owner 的图片里裁出码本身，带中心头像和点赞图标，四周留白，不要标题和黄条：
   ```
   node tools/crop-image.mjs --in <微信赞赏码.jpg> --out <wechat-code.jpg> --box 275,141,600 --size 384 --jpeg 92
   ```
   `--box` 是原图上正方形的 X,Y,边长（像素），上面的数值对应现在这张 1152×1152 的图。先看一眼输出再继续。
3. `task site:qr -- --wechat <wechat-code.jpg> --alipay <支付宝收款码.jpg> --embed-on-fail`（在 `site/` 下运行，所以给绝对路径）。支付宝码会被解码，重画成清晰的 SVG，不带头像和装饰；工具会把重画的码再解码一遍，不一致就停下。微信那张解码不了，`--embed-on-fail` 把它作为 `data:` JPEG 嵌入。工具改写 `src/qr-codes.js` 和 `qr/payloads.json`。
4. 用 `task site:dev` 看页面，提交这两个文件。owner 的原图永远不提交。

在运行过这一步之前，`src/qr-codes.js` 是占位，`task site:deploy` 会拒绝。

## 改文案

两种语言的文字在 `src/page.js` 的 `COPY` 里：标题、描述、标语、介绍、按钮和 GitHub 卡片。两种语言一起改。打赏卡片只有中文，写在同一个文件的 `donateCard()` 里。GitHub 卡片的中文文案是有意这样写的，不要“纠正”。

标记和类名来自页面设计（Claude Design 项目，最终版 30）。要改外观，同时改 `src/style.js` 和 `src/page.js` 里的标记，让类名仍然对得上。

改完：`task site:test`，再用 `task site:dev` 看两种语言（`/?lang=zh`、`/?lang=en`）。

## 部署与回滚

owner 说“发布”才部署，它会改线上的站点。用的是本机已登录的 wrangler，仓库里没有凭据。

```
task site:dry-run      # 先看
task site:deploy
```

回滚：在 `site/` 下运行 `npx wrangler@4.151.1 rollback`，让 Worker 的上一个版本重新上线。有页面之前的 Worker 也在 git 历史里：`git show 2dbf244c:app/scripts/download-worker/index.js`。两种情况下 `/latest` 路由都可以留着：旧 Worker 对路由到它的每个路径都回答同一个 302。

## 路由绝不能是 `/*`

`wrangler.toml` 的路由恰好是 `padmate.download/` 和 `padmate.download/latest`。`/*` 会把 Worker 放到 `latest.yml` 和安装包前面，它一出错，所有已安装的 padmate 都更新不了。不要加这样的路由，也不要为 R2 提供的文件加路由。
