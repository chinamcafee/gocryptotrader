#!/usr/bin/env node
/** 一条命令同时起本地代理与 Vite 开发服务器。Ctrl+C 会一并关闭。 */
import { spawn } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const procs = []

function run(name, cmd, args) {
  const p = spawn(cmd, args, { cwd: ROOT, stdio: ['ignore', 'pipe', 'pipe'], env: process.env })
  const tag = `[${name}]`
  const pipe = (stream, to) => stream.on('data', (d) => String(d).split('\n').filter(Boolean).forEach((l) => to.write(`${tag} ${l}\n`)))
  pipe(p.stdout, process.stdout)
  pipe(p.stderr, process.stderr)
  p.on('exit', (code) => {
    console.log(`${tag} 退出，code=${code}`)
    shutdown()
  })
  procs.push(p)
  return p
}

function shutdown() {
  for (const p of procs) { if (!p.killed) p.kill('SIGTERM') }
  process.exit(0)
}
process.on('SIGINT', shutdown)
process.on('SIGTERM', shutdown)

run('proxy', process.execPath, [path.join(ROOT, 'proxy', 'gct-proxy.mjs')])
run('vite', process.execPath, [path.join(ROOT, 'node_modules', 'vite', 'bin', 'vite.js')])
console.log('\n前端： http://127.0.0.1:5173/   代理： http://127.0.0.1:8788/api/health\n')
