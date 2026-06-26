package moduleregistry

import "errors"

// ErrEmptyID 在 descriptor 的 ID 为空时由 Register 返回。一个无法寻址的
// 模块是编程错误(它永远无法被 Get 或展示给运维人员),因此被拒绝而非
// 被静默存储。
var ErrEmptyID = errors.New("moduleregistry: descriptor ID must not be empty")
