// HUAKAI 官网首页 —— 由 Owner 的 Claude Design 产出(website kit)移植落地。
// 浅天蓝 + 柠檬绿主题;Hero 为 POST /v1/chat/completions SSE 终端 + 浮动 dispatch 卡。
// 各分区在 ./sections/*,共享原语在 ./landingKit,主题样式在 ./landingStyles。
// 整页包在 .hk-site 内:主题 token 作用域限本页,不污染 app 其它(暗色)主题。
import { LANDING_CSS } from './landingStyles'
import { SiteNav } from './sections/SiteNav'
import { SiteHero } from './sections/SiteHero'
import { SiteProviders } from './sections/SiteProviders'
import { SiteFeatures } from './sections/SiteFeatures'
import { SiteDeploy } from './sections/SiteDeploy'
import { SiteFooter } from './sections/SiteFooter'

export function LandingPage(): JSX.Element {
  return (
    <div className="hk-site">
      <style>{LANDING_CSS}</style>
      <SiteNav />
      <main>
        <SiteHero />
        <SiteProviders />
        <SiteFeatures />
        <SiteDeploy />
      </main>
      <SiteFooter />
    </div>
  )
}
