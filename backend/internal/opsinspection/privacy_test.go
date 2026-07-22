package opsinspection

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

// sentinel 是绝不应出现在报告正文中的值。它代表一个密钥/原始请求体/PII，
// 有缺陷的投影可能会把它从上游行的自由格式字段中泄露出来。
const sentinel = "sk-LEAK-SECRET-customer@example.com-RAWBODY-9f3a"

// TestPrivacyNoLeakIntoBody 把 sentinel 注入巡检读取的每一个上游自由格式/
// 身份字段（租户名、账号名、DLQ 失败原因 + 原始 payload、用量流终止原因、
// 模块探针 detail）。它断言 sentinel 不出现在渲染后的 HTML + 纯文本正文中，
// 也不出现在记录的运行结果里。
//
// 变异证明：若投影被改坏成把某个自由格式字段直接拷进某一段（例如把 DLQ
// failure_reason、续期租户名或探针 Detail 暴露出来），sentinel 就会出现在正文里，
// 该测试会变红。当前投影只保留 enums/counts/ids，所以它保持绿色。
func TestPrivacyNoLeakIntoBody(t *testing.T) {
	leakReason := sentinel
	src := Sources{
		ChannelSummary: func(_ context.Context, _ int64) (channelhealth.ChannelHealthSummary, error) {
			// 渠道汇总仅有聚合计数——没有可投毒的字符串字段，
			// 但带上一个无害的状态以便该段能正常组合出来。
			return channelhealth.ChannelHealthSummary{
				Total:   1,
				ByState: map[channelhealth.HealthState]int64{channelhealth.StateActive: 1},
			}, nil
		},
		RenewStatus: func(_ context.Context, _ credentialstore.ListRenewStatusParams) ([]credentialstore.RenewStatusMetadata, error) {
			// 对投影必须丢弃的身份/自由格式字段进行投毒。
			row := credentialstore.RenewStatusMetadata{
				CredentialID: 1,
				TenantName:   sentinel,
				AccountName:  sentinel,
				State:        "active",
				FailureClass: &leakReason, // 这里连失败分类也是个被投毒的值
				FailureCount: 0,
			}
			return []credentialstore.RenewStatusMetadata{row}, nil
		},
		DLQList: func(_ context.Context, _ dlq.ListFilter) ([]dlq.Record, error) {
			rec := dlq.Record{
				ID:            1,
				Lane:          dlq.LaneHigh,
				Status:        dlq.StatusDelivered,
				FailureReason: sentinel,                           // 自由格式的失败文本
				Payload:       []byte(`{"x":"` + sentinel + `"}`), // 原始请求体
				FailureAt:     fixedNow(),
			}
			return []dlq.Record{rec}, nil
		},
		ListUsage: func(_ context.Context, _ dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
			reason := sentinel
			row := dbbilling.ListUsageRecordsRow{
				ID: 1, TenantID: testTenant, EndClass: "ok", StreamTerminatedReason: &reason,
			}
			return []dbbilling.ListUsageRecordsRow{row}, nil
		},
		Modules: fakeSnapshotter{snaps: nil},
	}

	svc := NewInspectionService(src, testTenant, fixedNow)
	rep := svc.Inspect(context.Background())
	formatted := Format(rep)

	if strings.Contains(formatted.HTMLBody, sentinel) {
		t.Fatalf("sentinel leaked into HTML body")
	}
	if strings.Contains(formatted.PlainBody, sentinel) {
		t.Fatalf("sentinel leaked into plaintext body")
	}
	if strings.Contains(formatted.Subject, sentinel) {
		t.Fatalf("sentinel leaked into subject")
	}

	// 同时断言它绝不会进入记录的运行结果（其中只携带 enums/counts——没有正文、
	// 没有错误文本）。
	var captured RunOutcome
	rec := captureRecorder{out: &captured}
	rec.RecordRun(context.Background(), RunOutcome{
		IssueCount:   rep.IssueCount(),
		SourceErrors: len(rep.SourceErrors),
		FailureClass: "send_failed",
	})
	if strings.Contains(captured.FailureClass, sentinel) {
		t.Fatalf("sentinel leaked into recorded outcome")
	}
}

type captureRecorder struct{ out *RunOutcome }

func (c captureRecorder) RecordRun(_ context.Context, o RunOutcome) { *c.out = o }
