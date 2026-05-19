// Package pool 暴露账号池选择的根级公开契约。
//
// 具体实现按职责下沉到子包：
// router 负责 DefaultSelector、PASR、HRW ring、段 locality 和 aging；
// scoring 负责 score blend 与 miss-demote helper；binding 负责 sticky 和
// claim gate adapter；dispatcher 负责 mode dispatch、fallback/retry 判定、
// slot/account DB adapter 和 dispatch metrics。
package pool
