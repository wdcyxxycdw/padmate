// Deploy guard: refuses to go on while the donate card still has placeholder
// QR codes. `task site:deploy` runs it first, so the live page can never show
// two grey "收款码" boxes by accident. It has no dependencies on purpose: it
// must run on a fresh clone before `npm install`.

import { QR_CODES } from "../src/qr-codes.js";

if (!QR_CODES.wechat || !QR_CODES.alipay) {
  console.error(
    "错误：微信或支付宝收款码仍是占位符，先运行 `task site:qr -- --wechat <图片> --alipay <图片>` 再部署。\n" +
      "error: the WeChat and/or Alipay QR code is still a placeholder; " +
      "run `task site:qr -- --wechat <image> --alipay <image>` before deploying.",
  );
  process.exit(1);
}
