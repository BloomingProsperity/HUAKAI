// Package platformsettings 负责平台级的 admin 设置。
//
// 边界说明:
//   - 此处的设置只允许放入白名单内的非机密值。不要加入 provider 凭据、
//     明文 token、密码或 bearer key。
//   - 本包只写入 platform_settings 与 admin 审计事件。
package platformsettings
