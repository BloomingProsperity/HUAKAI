// Package mequotahttp 暴露已认证用户的只读配额状态。
//
// 该读取返回窗口形态的配额维度 —— requests、cost_usd 和 tokens_estimated ——
// 每个维度作为各自的一个窗口,带 cap/consumed/remaining,并按 metric 标注。
// 并发(concurrency)被刻意排除:它是基于槽位(slot-based)的 metric,而非
// 窗口累加计数器,因此没有窗口行可投影;并发状态读取作为单独的后续项追踪。
package mequotahttp
