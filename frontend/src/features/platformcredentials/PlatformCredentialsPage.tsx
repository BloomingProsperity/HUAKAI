import { useState } from 'react'
import { AdminTokenSection } from './AdminTokenSection'
import { PlatformApiKeySection } from './PlatformApiKeySection'

type CredentialsTab = 'admin_tokens' | 'api_keys'

export function PlatformCredentialsPage() {
  const [tab, setTab] = useState<CredentialsTab>('admin_tokens')

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>平台凭证</h1>
          <p className="hk-sub">集中管理运营台令牌与平台代签 API Key；明文凭证只在创建成功后显示一次。</p>
        </div>
      </header>

      <div className="hk-seg" role="tablist" aria-label="平台凭证类型" style={{ alignSelf: 'flex-start' }}>
        <button
          type="button"
          role="tab"
          aria-selected={tab === 'admin_tokens'}
          className={tab === 'admin_tokens' ? 'is-on' : ''}
          onClick={() => setTab('admin_tokens')}
        >
          运维令牌
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={tab === 'api_keys'}
          className={tab === 'api_keys' ? 'is-on' : ''}
          onClick={() => setTab('api_keys')}
        >
          平台级 API Key
        </button>
      </div>

      {tab === 'admin_tokens' ? <AdminTokenSection /> : <PlatformApiKeySection />}
    </div>
  )
}
