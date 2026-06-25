import type { SiteConfig } from './types'

/*
 * 法律条款页纯逻辑(可单测)。
 *
 * 后端无独立 terms/privacy 文本 key(已核 platformsettings/types.go),只有站点公开品牌
 * 字段(site_name/site_footer/site_contact_info/site_doc_url,见 sitepublichttp/handler.go)。
 * 因此本页正文主体采用站点通用占位文案,并把站点真实名称回填到 {{name}} 占位上;
 * 站点真名缺失时回退到「本平台」。这样运营者改 site_name 后,条款主体名随之更新。
 *
 * 本模块只做:① 文档分节选择(用户协议 / 隐私政策)② 占位插值 ③ 联系/页脚行的可见性判定。
 * 不打印任何敏感信息;全部为纯字符串变换,便于变异测试。
 */

/** 文档分节标识。 */
export type LegalDocKey = 'terms' | 'privacy'

export interface LegalSection {
  /** 小节标题。 */
  heading: string
  /** 段落数组(每段一行,渲染层按 <p> 拆分)。可含 {{name}} 占位。 */
  paragraphs: string[]
}

export interface LegalDoc {
  key: LegalDocKey
  /** 标签页文案。 */
  tab: string
  /** 文档主标题。 */
  title: string
  sections: LegalSection[]
}

/** 兜底主体名:站点未配置 site_name 时使用。 */
export const FALLBACK_SITE_NAME = '本平台'

const PLACEHOLDER = '{{name}}'

/**
 * 用站点真实名称替换占位 {{name}}。空白名退回兜底名,避免出现空主体。
 * 判别核心:有名用名、无名用兜底,且必须全局替换(同段可能多处占位)。
 */
export function interpolate(text: string, siteName: string): string {
  const name = siteName.trim() || FALLBACK_SITE_NAME
  return text.split(PLACEHOLDER).join(name)
}

/** 静态占位条款集合(clean-room:通用条款语,不抄任何竞品标识符/原文)。 */
export const LEGAL_DOCS: ReadonlyArray<LegalDoc> = [
  {
    key: 'terms',
    tab: '用户协议',
    title: '用户协议',
    sections: [
      {
        heading: '一、服务说明',
        paragraphs: [
          '欢迎使用 {{name}}(以下简称「本服务」)。本服务为 API 中转聚合平台,向您提供模型调用转发、密钥管理与用量计费等能力。',
          '在使用本服务前,请您仔细阅读并充分理解本协议各条款。一旦您注册、登录或以其他方式使用本服务,即视为您已阅读并同意接受本协议的全部内容。',
        ],
      },
      {
        heading: '二、账号与密钥',
        paragraphs: [
          '您应妥善保管账号凭据与 API 密钥。因您自身保管不善导致的密钥泄露及由此产生的调用与费用,由您自行承担。',
          '您不得将账号或密钥转售、出借给第三方用于规避本服务的额度、计费或安全策略。',
        ],
      },
      {
        heading: '三、使用规范',
        paragraphs: [
          '您承诺不利用本服务从事违反所在地法律法规的活动,不上传或生成违法、侵权或危害公共利益的内容。',
          '{{name}} 有权在发现违规使用时,对相关账号采取限流、暂停或终止服务等措施,并保留追究责任的权利。',
        ],
      },
      {
        heading: '四、计费与额度',
        paragraphs: [
          '本服务按实际调用的用量计费,具体价格、额度与结算规则以平台内展示为准。预充值余额的使用以平台台账记录为准。',
          '因上游供应商价格、可用性变化,{{name}} 可能调整模型清单与计费标准,调整将通过平台公示。',
        ],
      },
      {
        heading: '五、免责与变更',
        paragraphs: [
          '本服务依赖第三方上游模型供应商,可能因上游中断、限流或政策变化导致服务波动,{{name}} 将尽力保障可用性但不对上游不可抗因素承担超出法律规定的责任。',
          '本协议可能不时更新,更新后将在平台公示;若您继续使用本服务,即视为接受变更后的协议。',
        ],
      },
    ],
  },
  {
    key: 'privacy',
    tab: '隐私政策',
    title: '隐私政策',
    sections: [
      {
        heading: '一、我们收集的信息',
        paragraphs: [
          '为提供本服务,{{name}} 会收集您主动提供的注册信息(如邮箱)以及使用过程中产生的必要数据(如调用记录、用量统计、计费台账)。',
          '我们不会在请求转发链路之外留存您调用内容的正文,仅按计费与风控所需记录元数据(如模型、token 数、时间戳)。',
        ],
      },
      {
        heading: '二、信息的使用',
        paragraphs: [
          '我们使用上述信息用于身份认证、用量计费、安全风控、故障排查与服务改进。',
          '未经您同意,{{name}} 不会将您的个人信息用于与本服务无关的目的。',
        ],
      },
      {
        heading: '三、信息的共享与披露',
        paragraphs: [
          '除为完成模型调用而向上游供应商转发必要请求外,我们不会向第三方出售或非法提供您的个人信息。',
          '在法律法规要求或为保护平台与用户合法权益的必要情形下,{{name}} 可能依法披露相关信息。',
        ],
      },
      {
        heading: '四、信息安全',
        paragraphs: [
          '我们采用合理的技术与管理措施保护您的信息安全,包括传输加密、访问控制与审计日志。',
          '尽管如此,任何系统都无法保证绝对安全,请您同时妥善保管自己的凭据。',
        ],
      },
      {
        heading: '五、您的权利',
        paragraphs: [
          '您有权查询、更正自己的账号信息,并可在符合平台规则的前提下注销账号。',
          '如对本隐私政策有任何疑问,您可通过本页底部的联系方式与 {{name}} 取得联系。',
        ],
      },
    ],
  },
]

/** 取默认文档(用户协议)。 */
export const DEFAULT_DOC_KEY: LegalDocKey = 'terms'

/**
 * 按 key 选择文档;未知 key 回退默认文档(永不返回 undefined,渲染层无需空判)。
 * 判别核心:未知 key 必须回退到 terms,而非透传/报错。
 */
export function selectDoc(key: string): LegalDoc {
  return LEGAL_DOCS.find((d) => d.key === key) ?? LEGAL_DOCS[0]
}

export interface FooterMeta {
  /** 页脚补充行(site_footer),空则不展示。 */
  footer: string
  /** 联系方式行(site_contact_info),空则不展示。 */
  contact: string
  /** 文档链接(site_doc_url),空则不展示。 */
  docUrl: string
}

/**
 * 从站点配置规整出页脚元信息,去除首尾空白;空字段保持空串供渲染层按真值过滤。
 * 判别核心:仅空白的字段必须归一为空串(不展示空联系块)。
 */
export function buildFooterMeta(cfg: SiteConfig): FooterMeta {
  return {
    footer: cfg.site_footer.trim(),
    contact: cfg.site_contact_info.trim(),
    docUrl: cfg.site_doc_url.trim(),
  }
}
