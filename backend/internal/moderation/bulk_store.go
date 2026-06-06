package moderation

import (
	"context"
	"strings"

	dbmoderation "github.com/BloomingProsperity/HUAKAI/internal/db/moderation"
)

func (s *SQLStore) BulkCreateKeywords(ctx context.Context, req BulkCreateKeywordsRequest) (BulkCreateResult, error) {
	result := newBulkCreateResult()
	if len(req.Items) > BulkImportMaxItems {
		return result, ErrBulkImportTooLarge
	}

	keywords := make([]string, 0, len(req.Items))
	reasonCodes := make([]string, 0, len(req.Items))
	enabledValues := make([]bool, 0, len(req.Items))
	for idx, item := range req.Items {
		keyword := strings.TrimSpace(item.Keyword)
		if keyword == "" {
			result.Errors = append(result.Errors, BulkItemError{Index: idx, Reason: "keyword_required"})
			continue
		}
		keywords = append(keywords, keyword)
		reasonCodes = append(reasonCodes, nonEmpty(strings.TrimSpace(item.ReasonCode), "keyword_match"))
		enabledValues = append(enabledValues, item.Enabled)
	}
	if len(keywords) == 0 {
		return result, nil
	}

	rows, err := s.q.BulkCreateModerationKeywords(ctx, dbmoderation.BulkCreateModerationKeywordsParams{
		TenantID:      req.TenantID,
		UpdatedBy:     stringPtr(req.UpdatedBy),
		Keywords:      keywords,
		ReasonCodes:   reasonCodes,
		EnabledValues: enabledValues,
	})
	if err != nil {
		return BulkCreateResult{}, err
	}
	result.Accepted = len(rows)
	result.SkippedDuplicate = len(keywords) - len(rows)
	return result, nil
}

func (s *SQLStore) BulkCreateHashes(ctx context.Context, req BulkCreateHashesRequest) (BulkCreateResult, error) {
	result := newBulkCreateResult()
	if len(req.Items) > BulkImportMaxItems {
		return result, ErrBulkImportTooLarge
	}

	hashHexes := make([]string, 0, len(req.Items))
	reasonCodes := make([]string, 0, len(req.Items))
	enabledValues := make([]bool, 0, len(req.Items))
	for idx, item := range req.Items {
		hashHex := strings.ToLower(strings.TrimSpace(item.HashHex))
		if !ValidSHA256Hex(hashHex) {
			result.Errors = append(result.Errors, BulkItemError{Index: idx, Reason: "invalid_hash_hex"})
			continue
		}
		hashHexes = append(hashHexes, hashHex)
		reasonCodes = append(reasonCodes, nonEmpty(strings.TrimSpace(item.ReasonCode), "hash_match"))
		enabledValues = append(enabledValues, item.Enabled)
	}
	if len(hashHexes) == 0 {
		return result, nil
	}

	rows, err := s.q.BulkCreateModerationHashes(ctx, dbmoderation.BulkCreateModerationHashesParams{
		TenantID:      req.TenantID,
		UpdatedBy:     stringPtr(req.UpdatedBy),
		HashHexes:     hashHexes,
		ReasonCodes:   reasonCodes,
		EnabledValues: enabledValues,
	})
	if err != nil {
		return BulkCreateResult{}, err
	}
	result.Accepted = len(rows)
	result.SkippedDuplicate = len(hashHexes) - len(rows)
	return result, nil
}

func newBulkCreateResult() BulkCreateResult {
	return BulkCreateResult{Errors: []BulkItemError{}}
}

func ValidSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}
