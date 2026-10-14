import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const srcDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), 'src')

// 开发期：前端 5173，本地代理 8788，代理再转发到 GoCryptoTrader REST 网关 9053。
// 之所以多一层代理而不是让 Vite 直接代理 9053：
//   1) REST 网关是自签 TLS + Basic Auth，凭据不能进浏览器（安全红线）
//   2) 代理层可以实施只读白名单与 LLM 端点未实现的确定性降级
export default defineConfig({
  plugins: [react()],
  resolve: {
    // tsconfig 的 paths 只对 tsc 生效；Vite 的依赖预扫描与 dev 解析需要显式 alias
    alias: { '@': srcDir },
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8788',
        changeOrigin: false,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
    chunkSizeWarningLimit: 1600,
  },
  define: {
    __APP_VERSION__: JSON.stringify('0.1.0'),
  },
})
