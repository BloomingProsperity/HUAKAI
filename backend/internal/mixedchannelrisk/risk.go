// Package mixedchannelrisk 只做同一 channel 内账号组合风险判定。
//
// 它不读取或保存 credential payload, 调用方只传入 provider/vendor/auth_mode
// 等非秘密元数据。
package mixedchannelrisk

import (
	"fmt"
	"strconv"
	"strings"
)

type Account struct {
	ID          int64
	ProviderID  int64
	ChannelID   int64
	AccountType string
	Vendor      string
	AuthMode    string
}

type RiskItem struct {
	Dimension         string `json:"dimension"`
	ExistingAccountID int64  `json:"existing_account_id"`
	ExistingValue     string `json:"existing_value"`
	CandidateValue    string `json:"candidate_value"`
	Message           string `json:"message"`
}

type Report struct {
	HighRisk bool       `json:"high_risk"`
	Items    []RiskItem `json:"items"`
}

func Evaluate(candidate Account, peers []Account) Report {
	var items []RiskItem
	for _, peer := range peers {
		if peer.ID > 0 && peer.ID == candidate.ID {
			continue
		}
		if peer.ChannelID != 0 && candidate.ChannelID != 0 && peer.ChannelID != candidate.ChannelID {
			continue
		}
		items = append(items, compareDimension(peer, candidate, "source", providerValue(peer.ProviderID), providerValue(candidate.ProviderID))...)
		items = append(items, compareDimension(peer, candidate, "vendor", normalizedValue(peer.Vendor), normalizedValue(candidate.Vendor))...)
		items = append(items, compareDimension(peer, candidate, "credential_type", credentialType(peer), credentialType(candidate))...)
	}
	return Report{HighRisk: len(items) > 0, Items: dedupe(items)}
}

func compareDimension(peer, candidate Account, dimension, existing, incoming string) []RiskItem {
	if existing == incoming {
		return nil
	}
	return []RiskItem{{
		Dimension:         dimension,
		ExistingAccountID: peer.ID,
		ExistingValue:     existing,
		CandidateValue:    incoming,
		Message:           messageFor(dimension, existing, incoming),
	}}
}

func credentialType(a Account) string {
	// auth_mode 比 account_type 更接近真实凭据形态; legacy 账号没有 auth_mode 时
	// 回退到 account_type。
	authMode := normalizedValue(a.AuthMode)
	accountType := normalizedValue(a.AccountType)
	if authMode != "" {
		return authMode
	}
	return accountType
}

func providerValue(id int64) string {
	if id <= 0 {
		return "unknown"
	}
	return "provider:" + strconv.FormatInt(id, 10)
}

func normalizedValue(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func messageFor(dimension, existing, incoming string) string {
	switch dimension {
	case "source":
		return fmt.Sprintf("same channel already contains a different provider source (%s -> %s)", existing, incoming)
	case "vendor":
		return fmt.Sprintf("same channel already contains a different credential vendor (%s -> %s)", existing, incoming)
	case "credential_type":
		return fmt.Sprintf("same channel already contains a different credential type (%s -> %s)", existing, incoming)
	default:
		return fmt.Sprintf("same channel already contains a different %s (%s -> %s)", dimension, existing, incoming)
	}
}

func dedupe(items []RiskItem) []RiskItem {
	seen := make(map[string]struct{}, len(items))
	out := make([]RiskItem, 0, len(items))
	for _, item := range items {
		key := strings.Join([]string{
			item.Dimension,
			strconv.FormatInt(item.ExistingAccountID, 10),
			item.ExistingValue,
			item.CandidateValue,
		}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}
