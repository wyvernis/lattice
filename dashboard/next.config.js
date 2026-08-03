/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  async rewrites() {
    const scheduler = process.env.SCHEDULER_URL || "http://127.0.0.1:8082";
    const registry = process.env.REGISTRY_URL || "http://127.0.0.1:8083";
    const gateway = process.env.GATEWAY_URL || "http://127.0.0.1:8080";
    const chaos = process.env.CHAOS_URL || "http://127.0.0.1:8085";
    return [
      { source: "/api/cluster", destination: `${scheduler}/v1/cluster` },
      { source: "/api/models", destination: `${registry}/v1/models` },
      { source: "/api/chaos/:path*", destination: `${chaos}/v1/chaos/:path*` },
      { source: "/api/infer", destination: `${gateway}/v1/chat/completions` },
    ];
  },
};

module.exports = nextConfig;
