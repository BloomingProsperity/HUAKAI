import { Link } from 'react-router-dom'
import { KeyUsageAnalytics } from '../usage/KeyUsageAnalytics'

/*
 * 公开「凭 API Key 查用量」页(免登录)。壳外路由 /key-usage,无 RequireAuth。
 * 复用 features/usage 的 KeyUsageAnalytics:它自带 API Key 输入,所有查询走显式 bearer,
 * 不依赖 session。持 Key 者即可查该 Key 的用量(Key 本身是凭证),对标 sub2 的免登录查询页。
 * 零后端改动:/v1/me/usage、/v1/me/analytics/time-series、/v1/generation 均接受 API Key Bearer。
 */
export function PublicKeyUsagePage() {
  return (
    <div className="hk-public">
      <header className="hk-public__head">
        <div>
          <h1 style={{ margin: 0, fontSize: 20, color: 'var(--hk-ink-900)' }}>用量查询</h1>
          <p className="hk-sub" style={{ marginTop: 4 }}>
            无需登录 —— 粘贴你的 API Key(hk_…)即可查看该 Key 的用量、时间序列与逐笔明细。Key 仅保留在本页内存,不上传、不保存。
          </p>
        </div>
        <Link to="/login" className="hk-btn hk-btn--sm">登录管理端</Link>
      </header>

      <KeyUsageAnalytics />

      <footer className="hk-public__foot">
        数据来自平台真实计费用量,严格限定到所提供的 API Key 与其所属身份。
      </footer>
    </div>
  )
}
