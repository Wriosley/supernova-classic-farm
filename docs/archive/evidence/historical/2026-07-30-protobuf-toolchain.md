---
status: verified
date: 2026-07-30
evidence_type:
  - code
  - runtime
---

# Protobuf 双端生成与 Round-Trip 证据

## 目标

验证 accepted HTTP、WebSocket、数据模型和奖励邮件事件契约可以由同一组 `.proto` 生成 Go 与 TypeScript 类型，并完成最小二进制往返。

## 环境

- Windows 10 build 26200
- Go 1.26.4
- Node.js 20.20.0
- protoc 35.1
- Buf 1.72.0

本机没有可用的 Docker 命令；该事实不影响本项纯协议验证。

## 执行

在仓库根目录运行：

```powershell
powershell -ExecutionPolicy Bypass -File "proto\scripts\smoke.ps1"
```

脚本执行：

1. `buf lint`
2. `buf generate`
3. `go test ./gen/smoke`
4. `tsx src/gen/smoke/roundtrip.ts`

观察结果：

```text
Generated Go types in server/gen and TypeScript types in web/src/gen.
ok github.com/Wriosley/supernova-classic-farm/server/gen/smoke
TypeScript Protobuf round-trip smoke test passed
```

另行执行：

```powershell
cd web
npm run build
```

Vue TypeScript 骨架构建成功。

## 结论与限制

该结果证明当前共享 Proto 可以生成并在两个目标语言运行时完成最小序列化往返。它不证明 HTTP/WS 服务已实现、跨语言网络互通、业务正确性、存储正确性或任何性能能力。
