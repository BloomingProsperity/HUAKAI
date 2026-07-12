package main

import "testing"

// TestLoadSessionDevicePolicyFromEnv 守护新设备策略两旋钮的解析语义:
//   - 默认 (两 env 都不设) = 休眠 (max=0, policy=""), 零生产行为变更;
//   - 合法 policy ("" / revoke_oldest / confirm) + 合法 >=0 整数 max 正常解析;
//   - 非法 max (非数字 / 负数) 与未知 policy 一律 fail-loud 返 error 拒启。
//
// 变异 (§14): 把 max<0 的 fail-loud 改成静默 max=0, 则 "-1" 用例由"期望 err"变"无 err"→ RED;
//   把未知 policy 的 default 分支 return error 改成 return nil, 则 "bogus" 用例 RED。
//   (这两条直接守护"非法配置不静默回落休眠"——否则运维以为开了策略实则没开。)
func TestLoadSessionDevicePolicyFromEnv(t *testing.T) {
	cases := []struct {
		name       string
		maxEnv     string
		policyEnv  string
		wantMax    int
		wantPolicy string
		wantErr    bool
	}{
		{name: "默认休眠", maxEnv: "", policyEnv: "", wantMax: 0, wantPolicy: "", wantErr: false},
		{name: "confirm策略", maxEnv: "2", policyEnv: "confirm", wantMax: 2, wantPolicy: "confirm", wantErr: false},
		{name: "revoke_oldest策略", maxEnv: "3", policyEnv: "revoke_oldest", wantMax: 3, wantPolicy: "revoke_oldest", wantErr: false},
		{name: "max0显式", maxEnv: "0", policyEnv: "", wantMax: 0, wantPolicy: "", wantErr: false},
		{name: "max非数字fail-loud", maxEnv: "abc", policyEnv: "", wantErr: true},
		{name: "max负数fail-loud", maxEnv: "-1", policyEnv: "", wantErr: true},
		{name: "未知policy_fail-loud", maxEnv: "2", policyEnv: "bogus", wantErr: true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("HUAKAI_SESSION_MAX_ACTIVE_DEVICES", c.maxEnv)
			t.Setenv("HUAKAI_SESSION_DEVICE_POLICY", c.policyEnv)
			gotMax, gotPolicy, err := loadSessionDevicePolicyFromEnv()
			if c.wantErr {
				if err == nil {
					t.Fatalf("loadSessionDevicePolicyFromEnv() max=%q policy=%q want error, got nil", c.maxEnv, c.policyEnv)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadSessionDevicePolicyFromEnv() unexpected error: %v", err)
			}
			if gotMax != c.wantMax || gotPolicy != c.wantPolicy {
				t.Fatalf("loadSessionDevicePolicyFromEnv() = (%d, %q), want (%d, %q)", gotMax, gotPolicy, c.wantMax, c.wantPolicy)
			}
		})
	}
}
