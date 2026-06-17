// 内容审核黑名单——纯解析/校验辅助(零依赖)。
// 单独成文件的原因:这些是批量导入 / 哈希格式校验的行为关键逻辑,需可独立单测
// (node --experimental-strip-types 无法 import 含 ./client 的 moderation.ts)。moderation.ts re-export 之。

// 64 位小写十六进制校验(对齐后端 hash_hex 约束:invalid_hash_hex —— 后端要求恰好 64 位小写 hex)。
// 锚点 ^$ + 固定长度 {64} 缺一不可:大写、含非 hex 字符、长度 ≠64 都必须判 false,
// 否则非法哈希会被放行到后端再被拒(白跑一趟)或污染列表。
export function isValidHashHex(s: string): boolean {
  return /^[0-9a-f]{64}$/.test(s);
}

// 批量文本解析:每行一项,去空行 + 去首尾空白。
// 空行/纯空白行必须被剔除,否则会向后端提交空 keyword/空 hash 项(被拒或污染)。
export function parseBulkLines(text: string): string[] {
  return text
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => l.length > 0);
}

export function fmtDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false });
}
