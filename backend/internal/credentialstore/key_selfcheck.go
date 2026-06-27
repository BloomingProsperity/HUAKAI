package credentialstore

import (
	"context"
	"errors"
	"fmt"
)

// ErrKeySelfCheckFailed 表示启动期自检发现当前 KEK 无法解密既有凭证(密钥不在密钥环或材料不符)。
// 网关据此 fail-closed 拒绝启动,而非在运行时让全 relay 静默解密瘫痪。
var ErrKeySelfCheckFailed = errors.New("credentialstore: credential key self-check failed")

// 自检抽样的不同 provider_account 上限。抽多条(而非单条)是为避免被单个歧义 / 边界态账号致盲:
// 只要样本里有一条能解开就证明当前 KEK 整体可用。8 条足以覆盖"整体换错 KEK"这一全局灾难。
const keySelfCheckSampleLimit = 8

// VerifyKeySelfCheck 启动期凭证密钥自检:抽样至多 keySelfCheckSampleLimit 条不同 provider_account 的
// active 凭证,用当前 KEK 逐一走真实解密路径试解,据结果决定是否放行启动:
//   - 样本里有任一条能解开 → KEK 整体可用 → 放行(nil)。这条短路也避免了"个别凭证数据损坏却
//     拖垮整个网关启动"的误杀:KEK 对其余账号有效时不因单条坏数据 fail-closed。
//   - 样本里无一条能解开,且至少一条以 ErrKeyUnavailable(key_id 不在密钥环)/ ErrDecryptFailed
//     (密钥材料不符 / AAD 不符)失败 → 这正是 operator 整体换错 KEK 的特征 → 返回包裹
//     ErrKeySelfCheckFailed 的错误,调用方据此 fail-closed 拒绝启动。
//   - 无 active 凭证(全新部署)/ 取样查询瞬时故障 / 样本全是非解密类不确定错误(歧义、刚被删等)
//     → nil 放行(自检无定论,不误杀启动;真正的 KEK 灾难会在多账号样本里以解密失败暴露)。
//
// 背景:KEK 目前为单版本。operator 一旦轮换密钥,既有凭证(用旧 key_id / 旧材料加密)将全部解密失败,
// 在运行时表现为"所有上游账号凭证不可用=全 relay 瘫痪"且无启动期信号。本自检把它前移成一声响亮的
// 启动失败。多版本密钥环(按 key_id 回退解密、在线 re-encrypt)是后续单独切片。
//
// 走 LoadForProviderAccountTest 是因为它对各类 auth_mode 通用、复用生产解密路径(decryptRecord)且无
// 审计副作用;LoadForRefresh 仅针对可刷新(OAuth)凭证、会跳过 API-key 凭证,不适合做通用 KEK 自检。
func (s *Store) VerifyKeySelfCheck(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	rows, err := s.db.Query(ctx, `
SELECT DISTINCT ON (provider_account_id) tenant_id, provider_account_id
FROM account_credentials
WHERE state = 'active'
ORDER BY provider_account_id, id
LIMIT $1`, keySelfCheckSampleLimit)
	if err != nil {
		return nil // 取样查询瞬时故障:不阻断启动。
	}
	type sampleAccount struct{ tenantID, providerAccountID int64 }
	var sample []sampleAccount
	for rows.Next() {
		var a sampleAccount
		if scanErr := rows.Scan(&a.tenantID, &a.providerAccountID); scanErr != nil {
			rows.Close()
			return nil
		}
		sample = append(sample, a)
	}
	rows.Close()
	if rows.Err() != nil || len(sample) == 0 {
		return nil // 无 active 凭证(全新部署)或读取错误:放行。
	}

	var firstDecryptErr error
	var firstFailedAccount int64
	for _, a := range sample {
		if _, loadErr := s.LoadForProviderAccountTest(ctx, a.tenantID, a.providerAccountID); loadErr == nil {
			return nil // 有一条能解开 → KEK 整体可用,放行。
		} else if errors.Is(loadErr, ErrKeyUnavailable) || errors.Is(loadErr, ErrDecryptFailed) {
			if firstDecryptErr == nil {
				firstDecryptErr = loadErr
				firstFailedAccount = a.providerAccountID
			}
		}
		// 其它错误(歧义 ErrCredentialAmbiguous / 刚被删 / 瞬时):跳过,试下一条。
	}
	if firstDecryptErr != nil {
		// 样本无一条能解开且出现解密失败 = operator 整体换错 KEK 的特征 → fail-closed。
		return fmt.Errorf("%w: 当前 KEK 无法解密任一既有 active 凭证(首个失败 provider_account_id=%d): %v;请检查凭证密钥配置,切勿在无多版本密钥环时轮换 KEK", ErrKeySelfCheckFailed, firstFailedAccount, firstDecryptErr)
	}
	return nil // 样本全是非解密类不确定错误:无定论,放行。
}
