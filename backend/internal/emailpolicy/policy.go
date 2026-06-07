package emailpolicy

import (
	"errors"
	"strings"
)

var (
	ErrEmailDomainNotAllowed = errors.New("emailpolicy: email domain not allowed")
	ErrEmailAliasNotAllowed  = errors.New("emailpolicy: email alias not allowed")
	ErrEmailReserved         = errors.New("emailpolicy: email local-part reserved")
)

func CheckDomain(email string, enabled bool, csvList string) error {
	if !enabled {
		return nil
	}
	domain := emailDomain(email)
	if domain == "" {
		return ErrEmailDomainNotAllowed
	}
	for _, item := range csvSet(csvList, true) {
		if domain == item {
			return nil
		}
	}
	return ErrEmailDomainNotAllowed
}

func CheckAlias(email string, enabled bool) error {
	if !enabled {
		return nil
	}
	local := emailLocalPart(email)
	if strings.ContainsAny(local, "+.") {
		return ErrEmailAliasNotAllowed
	}
	return nil
}

func CheckReserved(email string, csvReserved string) error {
	local := strings.ToLower(emailLocalPart(email))
	if local == "" {
		return nil
	}
	for _, item := range csvSet(csvReserved, false) {
		if local == item {
			return ErrEmailReserved
		}
	}
	return nil
}

func emailDomain(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return email[at+1:]
}

func emailLocalPart(email string) string {
	email = strings.TrimSpace(email)
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return email
	}
	return email[:at]
}

func csvSet(raw string, trimDomainPrefix bool) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.ToLower(strings.TrimSpace(part))
		if trimDomainPrefix {
			item = strings.TrimPrefix(item, "@")
		}
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}
