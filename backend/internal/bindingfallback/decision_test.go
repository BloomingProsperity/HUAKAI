package bindingfallback

import (
	"reflect"
	"testing"
)

func TestClassContractAndFixedFallbackOrder(t *testing.T) {
	if ClassNormal != "normal" ||
		ClassContextWindow != "context_window" ||
		ClassSafety != "safety" ||
		ClassQuota != "quota" ||
		ClassManual != "manual" {
		t.Fatalf("class 常量与管理契约不一致：normal=%q context=%q safety=%q quota=%q manual=%q",
			ClassNormal, ClassContextWindow, ClassSafety, ClassQuota, ClassManual)
	}

	want := []Class{ClassContextWindow, ClassSafety, ClassQuota, ClassManual}
	got := FallbackClasses()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback class 固定顺序=%v，期望 %v", got, want)
	}
	got[0] = ClassManual
	if again := FallbackClasses(); !reflect.DeepEqual(again, want) {
		t.Fatalf("FallbackClasses 返回了可污染的共享切片：%v", again)
	}
	if NormalizeClass("") != ClassNormal || NormalizeClass("normal") != ClassNormal {
		t.Fatal("空 class 与显式 normal 必须归约为同一个主类")
	}
}

func TestTriggerReductionTableCoversEveryFallbackSignal(t *testing.T) {
	tests := []struct {
		name   string
		signal Signal
		class  Class
	}{
		{name: "绑定并发饱和", signal: SignalBindingConcurrencyLimit, class: ClassQuota},
		{name: "绑定RPM饱和", signal: SignalBindingRateLimit, class: ClassQuota},
		{name: "池内纯容量耗尽", signal: SignalPoolCapacityExhausted, class: ClassQuota},
		{name: "全部渠道降级", signal: SignalAllChannelsDegraded, class: ClassQuota},
		{name: "排队超时", signal: SignalQueueWaitTimeout, class: ClassQuota},
		{name: "排队溢出", signal: SignalQueueWaitOverflow, class: ClassQuota},
		{name: "上游限流", signal: SignalUpstreamRateLimit, class: ClassQuota},
		{name: "本地上下文窗口", signal: SignalLocalContextWindow, class: ClassContextWindow},
		{name: "上游精确上下文窗口", signal: SignalUpstreamContextWindow, class: ClassContextWindow},
		{name: "上游内容策略", signal: SignalUpstreamContentPolicy, class: ClassSafety},
		{name: "上游服务错误", signal: SignalUpstreamServerError, class: ClassManual},
		{name: "通用连接故障", signal: SignalTransientConnectionFailure, class: ClassManual},
		{name: "连接超时", signal: SignalConnectTimeout, class: ClassManual},
		{name: "首字节超时", signal: SignalFirstByteTimeout, class: ClassManual},
		{name: "上游超时", signal: SignalUpstreamTimeout, class: ClassManual},
		{name: "空响应", signal: SignalEmptyResponse, class: ClassManual},
	}
	if len(tests) != len(triggerRules) {
		t.Fatalf("触发归约表有 %d 行，判别测试只有 %d 行", len(triggerRules), len(tests))
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := TargetClass(tc.signal)
			if !ok || got != tc.class {
				t.Fatalf("TargetClass(%q)=(%q,%v)，期望 (%q,true)", tc.signal, got, ok, tc.class)
			}
			if IsTerminal(tc.signal) {
				t.Fatalf("可降级信号 %q 被错误标为终态", tc.signal)
			}
			state := permissiveTransitionState()
			got, ok = AllowTransition(tc.signal, state)
			if !ok || got != tc.class {
				t.Fatalf("AllowTransition(%q)=(%q,%v)，期望 (%q,true)", tc.signal, got, ok, tc.class)
			}
		})
	}
}

func TestTerminalGateRejectsEveryNeverFallbackFamily(t *testing.T) {
	tests := []struct {
		name   string
		signal Signal
	}{
		{name: "key限额", signal: SignalKeyRateLimit},
		{name: "用户限额", signal: SignalUserQuota},
		{name: "租户限额", signal: SignalTenantQuota},
		{name: "token限额", signal: SignalTokenQuota},
		{name: "静态池策略不匹配", signal: SignalPoolStaticMismatch},
		{name: "客户端取消排队", signal: SignalQueueWaitCancelled},
		{name: "请求体过大", signal: SignalRequestBodyTooLarge},
		{name: "请求格式错误", signal: SignalRequestMalformed},
		{name: "本地审核拒绝", signal: SignalLocalModerationDenied},
		{name: "租户策略拒绝", signal: SignalTenantPolicyDenied},
		{name: "官方客户端门拒绝", signal: SignalOfficialClientDenied},
		{name: "权限拒绝", signal: SignalPermissionDenied},
		{name: "上游认证失败", signal: SignalUpstreamAuthFailure},
		{name: "计费预留失败", signal: SignalBillingReserveFailure},
		{name: "计费结算失败", signal: SignalBillingSettleFailure},
		{name: "计费中止失败", signal: SignalBillingAbortFailure},
		{name: "定价失败", signal: SignalPricingFailure},
		{name: "claim竞争", signal: SignalClaimConflict},
		{name: "指纹冲突", signal: SignalFingerprintConflict},
		{name: "本地配置错误", signal: SignalLocalConfigurationFailure},
		{name: "本地凭据解析错误", signal: SignalCredentialResolutionFailure},
		{name: "未知信号", signal: Signal("unknown")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := TargetClass(tc.signal); ok {
				t.Fatalf("终态信号 %q 不得映射目标 class", tc.signal)
			}
			if !IsTerminal(tc.signal) {
				t.Fatalf("信号 %q 必须 fail-closed 为终态", tc.signal)
			}
			if got, ok := AllowTransition(tc.signal, permissiveTransitionState()); ok {
				t.Fatalf("终态信号 %q 错误进入 %q", tc.signal, got)
			}
		})
	}
}

func TestBoundaryPairsDoNotCollapseByHTTPStatus(t *testing.T) {
	tests := []struct {
		name     string
		positive Signal
		negative Signal
		class    Class
	}{
		{name: "binding rate不等于key rate", positive: SignalBindingRateLimit, negative: SignalKeyRateLimit, class: ClassQuota},
		{name: "上游内容策略403不等于本地moderation403", positive: SignalUpstreamContentPolicy, negative: SignalLocalModerationDenied, class: ClassSafety},
		{name: "精确上下文错误不等于body 413", positive: SignalUpstreamContextWindow, negative: SignalRequestBodyTooLarge, class: ClassContextWindow},
		{name: "上游5xx不等于本地配置错误", positive: SignalUpstreamServerError, negative: SignalLocalConfigurationFailure, class: ClassManual},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := AllowTransition(tc.positive, permissiveTransitionState()); !ok || got != tc.class {
				t.Fatalf("正例 %q=(%q,%v)，期望 %q", tc.positive, got, ok, tc.class)
			}
			if got, ok := AllowTransition(tc.negative, permissiveTransitionState()); ok {
				t.Fatalf("反例 %q 错误进入 %q", tc.negative, got)
			}
		})
	}
}

func TestTransitionGateRequiresSingleUndeliveredNormalExhaustion(t *testing.T) {
	base := permissiveTransitionState()
	tests := []struct {
		name   string
		mutate func(*TransitionState)
	}{
		{name: "主类尚未耗尽", mutate: func(s *TransitionState) { s.PrimaryExhausted = false }},
		{name: "目标类未配置", mutate: func(s *TransitionState) { s.TargetConfigured = false }},
		{name: "已经交付字节", mutate: func(s *TransitionState) { s.DeliveryStarted = true }},
		{name: "已经跨类一次", mutate: func(s *TransitionState) { s.TransitionUsed = true }},
		{name: "当前已在quota", mutate: func(s *TransitionState) { s.CurrentClass = ClassQuota }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := base
			tc.mutate(&state)
			if got, ok := AllowTransition(SignalBindingConcurrencyLimit, state); ok {
				t.Fatalf("终态门被绕过，错误进入 %q", got)
			}
		})
	}

	emptyCurrent := base
	emptyCurrent.CurrentClass = ""
	if got, ok := AllowTransition(SignalBindingConcurrencyLimit, emptyCurrent); !ok || got != ClassQuota {
		t.Fatalf("空 current class 必须兼容归约为 normal，得到 (%q,%v)", got, ok)
	}
}

func TestTransitionGateAppliesTriggerSpecificConditions(t *testing.T) {
	noRetry := permissiveTransitionState()
	noRetry.RetryPermitted = false
	for _, signal := range []Signal{
		SignalUpstreamRateLimit,
		SignalUpstreamServerError,
		SignalTransientConnectionFailure,
		SignalConnectTimeout,
		SignalFirstByteTimeout,
		SignalUpstreamTimeout,
		SignalEmptyResponse,
	} {
		if got, ok := AllowTransition(signal, noRetry); ok {
			t.Fatalf("普通 retry 未获准时，信号 %q 错误进入 %q", signal, got)
		}
	}
	if got, ok := AllowTransition(SignalBindingConcurrencyLimit, noRetry); !ok || got != ClassQuota {
		t.Fatalf("D1 绑定容量不依赖普通上游 retry 开关，得到 (%q,%v)", got, ok)
	}

	localSafetyBlocked := permissiveTransitionState()
	localSafetyBlocked.LocalSafetyPassed = false
	if got, ok := AllowTransition(SignalUpstreamContentPolicy, localSafetyBlocked); ok {
		t.Fatalf("本地安全门未通过时错误进入 %q", got)
	}
	if got, ok := AllowTransition(SignalUpstreamContentPolicy, permissiveTransitionState()); !ok || got != ClassSafety {
		t.Fatalf("本地安全门通过且配置目标后应进入 safety，得到 (%q,%v)", got, ok)
	}
}

func permissiveTransitionState() TransitionState {
	return TransitionState{
		CurrentClass:      ClassNormal,
		PrimaryExhausted:  true,
		TargetConfigured:  true,
		RetryPermitted:    true,
		LocalSafetyPassed: true,
	}
}
