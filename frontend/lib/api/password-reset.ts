// 重置密码表单的纯校验(零依赖,可独立单测)。返回错误文案 string,或 null 表示通过。
// 规则与后端注册一致:至少 6 位;两次输入必须一致。
export const MIN_PASSWORD_LEN = 6;

export function validateNewPassword(pw: string, confirm: string): string | null {
  if (pw.length < MIN_PASSWORD_LEN) return `密码至少 ${MIN_PASSWORD_LEN} 位。`;
  if (pw !== confirm) return '两次输入的密码不一致。';
  return null;
}
