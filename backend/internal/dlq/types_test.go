package dlq

import "testing"

// TestLaneForKind_PostDeliverySettlementIsHigh 守 P2/P3 修复:
// post_delivery_settlement 代表"模型内容已交付客户端 + 钱账未落盘",
// money path 灰区,必须走 HIGH lane 让 worker 优先重放。
// 不能落到默认 MED 或被分到 LOW,否则在 DLQ 队列里被 metrics 等低优先级
// 事件挡住,会拉长钱账落盘窗口。
//
// Mutation:把 EventKindPostDeliverySettlement 从 HIGH case 删掉(回退
// 到 LaneMed default)→ 本用例必红。
func TestLaneForKind_PostDeliverySettlementIsHigh(t *testing.T) {
	got := LaneForKind(EventKindPostDeliverySettlement)
	if got != LaneHigh {
		t.Fatalf("LaneForKind(post_delivery_settlement) = %q, want HIGH (money-path delivered-but-not-settled belongs to highest lane)", got)
	}
}

func TestLaneForKind_CostReceiptAppendIsHigh(t *testing.T) {
	got := LaneForKind(EventKindCostReceiptAppend)
	if got != LaneHigh {
		t.Fatalf("LaneForKind(cost_receipt_append) = %q, want HIGH (settled money without receipt proof must replay before low-priority work)", got)
	}
}

// TestEventKindPostDeliverySettlement_StringValueMatchesSQLCheck 守 schema 0053:
// Go 常量字符串值必须跟 SQL CHECK constraint 的 'post_delivery_settlement'
// 字面值一致,否则 enqueue 时 INSERT 会被 CHECK 拒。
//
// Mutation:把常量改成 "post_delivery_settle" 等错拼 → 本用例必红。
func TestEventKindPostDeliverySettlement_StringValueMatchesSQLCheck(t *testing.T) {
	const expected = "post_delivery_settlement"
	if string(EventKindPostDeliverySettlement) != expected {
		t.Fatalf("EventKindPostDeliverySettlement = %q, want %q (must match 0053 SQL CHECK literal)", EventKindPostDeliverySettlement, expected)
	}
}

func TestEventKindCostReceiptAppend_StringValueMatchesSQLCheck(t *testing.T) {
	const expected = "cost_receipt_append"
	if string(EventKindCostReceiptAppend) != expected {
		t.Fatalf("EventKindCostReceiptAppend = %q, want %q (must match 0066 SQL CHECK literal)", EventKindCostReceiptAppend, expected)
	}
}

// TestReplicaStatusForKind_PostDeliverySettlementIsNone 守 default 行为:
// post_delivery_settlement 不走 replica delivery(它是 settle replay
// intent,不是 replica),所以 replica_status 应该是 'none'。
//
// Mutation:误把 EventKindPostDeliverySettlement 加到 replica='pending'
// case → 本用例必红。
func TestReplicaStatusForKind_PostDeliverySettlementIsNone(t *testing.T) {
	got := ReplicaStatusForKind(EventKindPostDeliverySettlement)
	if got != ReplicaStatusNone {
		t.Fatalf("ReplicaStatusForKind(post_delivery_settlement) = %q, want %q (settle replay is not a replica)", got, ReplicaStatusNone)
	}
}

func TestReplicaStatusForKind_CostReceiptAppendIsNone(t *testing.T) {
	got := ReplicaStatusForKind(EventKindCostReceiptAppend)
	if got != ReplicaStatusNone {
		t.Fatalf("ReplicaStatusForKind(cost_receipt_append) = %q, want %q (receipt replay is not replica delivery)", got, ReplicaStatusNone)
	}
}
