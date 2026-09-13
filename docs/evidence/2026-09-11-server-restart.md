# 2026-09-11 服务器重新启动验证

- 按用户请求重新下载依赖并启动现有服务器，未修改业务代码或 .env。
- 当前分支：class-mid。
- 使用进程级 GOPROXY=https://goproxy.cn,direct 执行 go mod download，权限审核后退出码 0。
- 首次沙箱启动因 Go 构建缓存写入权限失败；权限审核后使用现有 start-servers.ps1 后台启动成功。
- 启动日志确认：Data mode: in-memory；监听地址 http://127.0.0.1:8080。
- 2026-09-11 09:23:08 +08:00，GET /healthz 返回 HTTP 200，JSON code 为 OK。
- 启动后的服务进程 PID：27260。
- Get-NetTCPConnection 查询被拒绝访问；运行状态通过进程、启动日志与实际 HTTP 响应确认。
- 本次只验证依赖下载、编译启动与健康检查，未执行游戏业务回归或 MySQL 验证。内存模式退出后数据丢失。
- BAT 中文解析问题未修复，本次直接调用 PowerShell 启动脚本。
- AI 辅助记录：Codex 执行上述命令并记录真实结果。
