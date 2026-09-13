# class-mid 测试说明

本文只描述当前关系型后端实际存在并执行过的验证。旧版 MemoryStore、sqlmock、4 块地和请求去重测试已经随对应代码删除。

## Go 自动测试

当前保留两个简单的密码测试：

- `TestEncodePassword`：验证大小写字母、数字循环右移 3 位。
- `TestValidateCredentials`：验证用户名及字母数字密码格式。

运行：

```powershell
cd server
$env:GOCACHE="$PWD\..\.cache\go-build"
go test ./... -count=1
go vet ./...
```

当前业务验证主要使用真实 MySQL 接口链，而不是大量模拟 SQL 测试。

## 真实 MySQL 双玩家验证

先启动服务器，再在另一个终端运行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\demo-friends.ps1
```

脚本验证：

1. 注册并登录 A、B 两个临时玩家。
2. A 添加 B，好友列表为 1，好友农场返回 16 块地。
3. B 通过 WebSocket 买胡萝卜、种植、施肥。
4. 等待 21 秒成熟后，A 偷菜。
5. 检查 A 增加 1 金币和 3 个胡萝卜。
6. A 给 B 发邮件，并从服务端返回的 B 邮箱中核对标题与正文。

只验证好友与自定义邮件：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\demo-friends.ps1 `
  -MailOnly -MailTitle "测试标题" -MailContent "测试正文"
```

成功输出包含：

```text
Friend added: 26; friends: 1; plots: 16
B received mail: title=[测试标题] content=[测试正文]
```

## 六种作物验证

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\demo-all-crops.ps1
```

脚本为同一玩家分别购买并种植 1–6 号作物，等待 61 秒且每 20 秒发送心跳，然后逐种收获、核对产量并出售 1 个。全部成功时最后输出 `ALL_SIX_CROPS_OK`。

## Qt 验证说明

当前 Qt 主界面按 `plots` 数组动态显示，可以接收 16 块地。旧 `classic_farm_smoke` 把旧版初始 10 金币、1 肥料和 4 块地写死，因此在新规则下会报 `unexpected initial snapshot`。Qt 组员应按 [Qt 功能升级文档](qt-feature-upgrade.md)更新断言和数据模型后再作为新版冒烟测试。

## Vue 验证

如果修改 Vue，再运行：

```powershell
cd web
npm.cmd test
npm.cmd run build
```

本轮后端关系型改造没有修改 Vue，因此没有把旧 Vue 界面视为多作物和好友功能的验收证据。

## 未覆盖范围

- 尚无新版 Qt 好友页面和 Qt 好友自动测试。
- 没有并发压力测试、网络故障自动重试测试或公网安全测试。
- 没有好友申请确认、偷菜次数限制和邮件分页。
- 凯撒密码只验证编码正确性，不代表真实密码安全。

实际执行结果见 [2026-09-14 验证记录](evidence/2026-09-14-relational-backend.md)。
