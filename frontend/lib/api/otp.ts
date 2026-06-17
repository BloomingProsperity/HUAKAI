// 登录 2FA 验证码纯逻辑(零依赖,可独立单测)。
// 单独成文件:登录页含 React/客户端依赖无法在 node strip-types 下 import,这两个纯函数抽出可真测。

export const OTP_LENGTH = 6;

// 只保留数字并截断到 6 位:防止 one-time-code 自动填充/粘贴带进空格、连字符或超长串。
// 这是"满位自动提交"判定的前置——若不截断,粘贴 "123456789" 会一直不等于 6 位、永不自动提交。
export function sanitizeOtp(raw: string): string {
  return raw.replace(/\D/g, '').slice(0, OTP_LENGTH);
}

// 恰好 6 位数字才算填满(触发自动提交)。
export function isOtpComplete(code: string): boolean {
  return /^\d{6}$/.test(code);
}
