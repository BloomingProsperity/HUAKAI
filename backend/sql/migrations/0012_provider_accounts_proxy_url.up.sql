-- 0012_provider_accounts_proxy_url.up.sql
--
-- 给 provider_accounts 表加 proxy_url 列，用于 PostgresProxyResolver 的
-- 账号级出站代理路由。
--
-- 语义（与 provider.ProxyResolver 接口对齐）：
--   - 行存在 + proxy_url IS NULL    → 已注册，明确直连（不用代理）
--   - 行存在 + proxy_url 非空        → 使用该代理 URL 出站
--   - 行不存在                       → ErrAccountNotFound（应用层报错）
--
-- proxy_url 文本格式：完整 URL，如
--   http://proxy.example.com:3128
--   socks5://user:pass@proxy.example.com:1080
--   https://outbound-proxy.internal:443
-- 解析由应用层 (net/url.Parse) 完成；DB 不做格式校验，避免与 Go 解析规则
-- 不一致。

BEGIN;

ALTER TABLE provider_accounts
    ADD COLUMN IF NOT EXISTS proxy_url text;

COMMENT ON COLUMN provider_accounts.proxy_url IS
    '账号级出站代理 URL。NULL 表示直连。'
    'PostgresProxyResolver 读取此列，配合 transport.WrapTransportWithProxy 注入到 RoundTripper。'
    '格式：完整 URL（含 scheme + host + 可选 port）；解析在应用层用 net/url.Parse 完成。';

COMMIT;
