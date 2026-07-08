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
			name: "官方 originator 放行",
			cand: Candidate{UserAgent: "curl/8", Originator: "codex_cli_rs"},
			want: Decision{Allow: true, Reason: ReasonMatchedOfficialOriginator},
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
