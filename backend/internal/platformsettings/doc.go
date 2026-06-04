// Package platformsettings owns platform-wide admin settings.
//
// Boundary notes:
//   - settings here are allow-listed non-secret values only. Do not add
//     provider credentials, plaintext tokens, passwords, or bearer keys.
//   - This package writes only platform_settings and admin audit events.
package platformsettings
