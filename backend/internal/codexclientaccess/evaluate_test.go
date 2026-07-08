package codexclientaccess

import "testing"

func TestEvaluateBranches(t *testing.T) {
	tests := []struct {
		name   string
		policy Policy
		cand   Candidate
		want   Decision
	}{
		{
			name: "force 越过黑名单",
			policy: Policy{
				ForceAllow: true,
				Blacklist:  []AllowedClientEntry{{UAContains: []string{"curl"}}},
			},
			cand: Candidate{UserAgent: "curl/8"},
			want: Decision{Allow: true, Reason: ReasonForceAllow},
		},
		{
			name:   "黑名单拒绝",
			policy: Policy{Blacklist: []AllowedClientEntry{{UAContains: []string{"curl"}}}},
			cand:   Candidate{UserAgent: "curl/8"},
			want:   Decision{Allow: false, Reason: ReasonBlacklisted},
		},
		{
			name: "官方 UA 放行",
			cand: Candidate{UserAgent: "codex_cli_rs/0.41.0"},
			want: Decision{Allow: true, Reason: ReasonMatchedOfficialUA},
		},
		{
			// originator 命中放行仍需 UA 带可解析引擎版本(版本门硬要求,防单因子伪造)。
			name: "官方 originator 放行",
			cand: Candidate{UserAgent: "cccc/1.2.3 (linux; x86_64) xterm", Originator: "codex_cli_rs"},
			want: Decision{Allow: true, Reason: ReasonMatchedOfficialOriginator},
		},
		{
			name: "官方 originator 但 UA 版本不可检测拒绝",
			cand: Candidate{UserAgent: "curl/8.0", Originator: "codex_cli_rs"},
			want: Decision{Allow: false, Reason: ReasonVersionUndetectable},
		},
		{
			name:   "官方 UA 低于最小版本拒绝",
			policy: Policy{MinVersion: "0.142.0"},
			cand:   Candidate{UserAgent: "codex_cli_rs/0.141.0 (Ubuntu 22.4.0; x86_64) xterm"},
			want:   Decision{Allow: false, Reason: ReasonVersionTooLow},
		},
		{
			name:   "官方 UA 高于最大版本拒绝",
			policy: Policy{MaxVersion: "0.140.0"},
			cand:   Candidate{UserAgent: "codex_cli_rs/0.141.0 (Ubuntu 22.4.0; x86_64) xterm"},
			want:   Decision{Allow: false, Reason: ReasonVersionTooHigh},
		},
		{
			name: "白名单候选不受版本门影响",
			policy: Policy{
				MinVersion: "99.0.0",
				Whitelist: []AllowedClientEntry{{
					Originator: "team_client",
					UAContains: []string{"team"},
				}},
			},
			cand: Candidate{UserAgent: "Team Client (no version)", Originator: "team_client"},
			want: Decision{Allow: true, Reason: ReasonMatchedWhitelist},
		},
		{
			name:   "app-server 候选不受版本门影响",
			policy: Policy{MinVersion: "99.0.0", AllowAppServer: true},
			cand:   Candidate{UserAgent: "curl/8.0"},
			want:   Decision{Allow: true, Reason: ReasonMatchedAppServer},
		},
		{
			name: "官方 UA 无版本边界照常放行",
			cand: Candidate{UserAgent: "codex_cli_rs/0.141.0 (Ubuntu 22.4.0; x86_64) xterm"},
			want: Decision{Allow: true, Reason: ReasonMatchedOfficialUA},
		},
		{
			// S1 修:strict 官方 UA(空格家族,头部无三段版本)未配版本边界时不因不可解析误拒。
			name: "官方 UA 空格家族无边界不误拒",
			cand: Candidate{UserAgent: "Codex Desktop/1.0"},
			want: Decision{Allow: true, Reason: ReasonMatchedOfficialUA},
		},
		{
			// 配了版本边界时,官方 UA 仍要求可解析版本(运维显式启用版本策略)。
			name:   "官方 UA 空格家族配边界不可解析拒",
			policy: Policy{MinVersion: "0.1.0"},
			cand:   Candidate{UserAgent: "Codex Desktop/1.0"},
			want:   Decision{Allow: false, Reason: ReasonVersionUndetectable},
		},
		{
			name: "白名单放行",
			policy: Policy{Whitelist: []AllowedClientEntry{{
				Originator: "team_client",
				UAContains: []string{"team", "client"},
			}}},
			cand: Candidate{UserAgent: "Team Client/1.0", Originator: "team_client"},
			want: Decision{Allow: true, Reason: ReasonMatchedWhitelist},
		},
		{
			name:   "app server 放行",
			policy: Policy{AllowAppServer: true},
			cand:   Candidate{UserAgent: "curl/8"},
			want:   Decision{Allow: true, Reason: ReasonMatchedAppServer},
		},
		{
			name: "都不命中拒绝",
			cand: Candidate{UserAgent: "curl/8"},
			want: Decision{Allow: false, Reason: ReasonNotMatched},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(tt.policy, tt.cand)
			if got != tt.want {
				t.Fatalf("Evaluate() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
