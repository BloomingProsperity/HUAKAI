// next.config.mjs 反代必须 env 驱动(HUAKAI_GATEWAY_URL),不得硬编码 localhost——否则非本地部署连不上后端。
// 配置文件用源码文本断言(同 dashboard-real-api.test.ts 范式)。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

const ROOT = process.cwd();
const cfg = readFileSync(join(ROOT, 'next.config.mjs'), 'utf8');

test('TestNextRewrites_EnvDrivenNotHardcoded', () => {
  // 网关地址必须来自环境变量。
  assert.match(cfg, /process\.env\.HUAKAI_GATEWAY_URL/, '应从 HUAKAI_GATEWAY_URL env 读网关地址');
  // 四条 rewrite 的 destination 都必须用 ${GATEWAY_URL} 模板(而非硬编码)。
  for (const p of ['/v1/', '/admin/v1/', '/debug/', '/.well-known/']) {
    const esc = p.replace(/[/.]/g, '\\$&');
    const re = new RegExp('destination:\\s*`\\$\\{GATEWAY_URL\\}' + esc + ':path\\*`');
    assert.match(cfg, re, `${p} 的 destination 应用 GATEWAY_URL 模板`);
  }
  // 判别:localhost:8080 只应作为 fallback 默认值出现 1 次;任一 destination 回退硬编码 → 计数 >1 → red。
  const hits = (cfg.match(/http:\/\/localhost:8080/g) || []).length;
  assert.equal(hits, 1, `localhost:8080 应只在 fallback 出现 1 次,实得 ${hits}`);
});
