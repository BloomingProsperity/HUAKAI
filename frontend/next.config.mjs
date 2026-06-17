/** @type {import('next').NextConfig} */

// 把 /v1/*、/admin/v1/*、/debug/* 反代到后端网关，规避浏览器 CORS 限制。
// 网关地址由 HUAKAI_GATEWAY_URL 环境变量驱动(缺省回落本地 :8080),与 lib/api/huakai.ts 同源;
// 之前这里硬编码 localhost:8080,任何非本地部署都连不上后端 —— 本次改为 env 驱动修复。
const GATEWAY_URL = process.env.HUAKAI_GATEWAY_URL || 'http://localhost:8080';

const nextConfig = {
  images: {
    unoptimized: true,
  },
  async rewrites() {
    return [
      {
        source: '/v1/:path*',
        destination: `${GATEWAY_URL}/v1/:path*`,
      },
      {
        source: '/admin/v1/:path*',
        destination: `${GATEWAY_URL}/admin/v1/:path*`,
      },
      {
        source: '/debug/:path*',
        destination: `${GATEWAY_URL}/debug/:path*`,
      },
      {
        source: '/.well-known/:path*',
        destination: `${GATEWAY_URL}/.well-known/:path*`,
      },
    ];
  },
};

export default nextConfig;
