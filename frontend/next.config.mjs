/** @type {import('next').NextConfig} */

// 把 /v1/*、/admin/v1/*、/debug/* 反代到后端 :8080，规避浏览器 CORS 限制。
// 开发环境 next dev 生效；生产部署时需 nginx/caddy 层做同样规则。
const nextConfig = {
  async rewrites() {
    return [
      {
        source: '/v1/:path*',
        destination: 'http://localhost:8080/v1/:path*',
      },
      {
        source: '/admin/v1/:path*',
        destination: 'http://localhost:8080/admin/v1/:path*',
      },
      {
        source: '/debug/:path*',
        destination: 'http://localhost:8080/debug/:path*',
      },
      {
        source: '/.well-known/:path*',
        destination: 'http://localhost:8080/.well-known/:path*',
      },
    ];
  },
};

export default nextConfig;
