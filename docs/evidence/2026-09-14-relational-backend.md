# 2026-09-14 关系型后端验证记录

## 环境与重建

- 分支：`class-mid`。
- 使用 `mysql --default-character-set=utf8mb4` 执行 `server/sql/reset_class_mid.sql`，执行成功。
- `SHOW TABLES LIKE 'class_mid_%'` 返回 9 张表。
- 查询 `class_mid_crops` 返回胡萝卜、玉米、土豆、番茄、草莓、南瓜 6 行，成熟时间分别为 30、35、40、45、50、60 秒。

## Go 验证

在 `server` 目录设置项目内 `GOCACHE` 后执行：

```powershell
go test ./... -count=1
go vet ./...
```

结果：`cmd/game` 无测试文件；`internal/game` 通过；`go vet` 无输出并以 0 退出。

## 真实双玩家链路

服务端连接本机 MySQL 后，执行 `scripts/demo-friends.ps1`。脚本使用随机用户名创建两个账号，通过 HTTP 登录，通过 WebSocket 为第二个玩家购买并种植 1 颗胡萝卜，再执行好友接口。

实际输出：

```text
Friend added: 5; friends: 1; plots: 16
Waiting 21 seconds for the fertilized carrot...
Steal succeeded: coins=51; carrots=3
B received mail: title=[CourseMail5] content=[HelloB5]
```

这证明了注册、登录、WebSocket 认证、买种子、种植、双向好友、查看好友 16 块地、成熟判断、偷菜增加金币和库存、目标地块更新及好友邮件写入。测试产生的临时账号保留在本机重建后的测试库中。

文档更新后又执行邮件快速验证：`-MailOnly -MailTitle "QtUpdateTest" -MailContent "CustomMessage"`。实际返回 `B received mail: title=[QtUpdateTest] content=[CustomMessage]`。发信接口只按新邮件 ID 和 B 的 ID 读回刚写入的一封，不返回 B 的其他邮件。

## 六种作物真实验证

执行 `scripts/demo-all-crops.ps1`，通过 WebSocket 分别购买和种植 1–6 号作物，等待最慢的南瓜成熟，然后逐种收获、核对库存产量并出售 1 个。实际结果：

```text
crop_id=1 name=胡萝卜 yield=3 sale_price=5 OK
crop_id=2 name=玉米 yield=3 sale_price=7 OK
crop_id=3 name=土豆 yield=4 sale_price=9 OK
crop_id=4 name=番茄 yield=4 sale_price=11 OK
crop_id=5 name=草莓 yield=5 sale_price=14 OK
crop_id=6 name=南瓜 yield=5 sale_price=18 OK
ALL_SIX_CROPS_OK
```

第一次脚本连续等待 61 秒导致 WebSocket 先达到 60 秒空闲超时；加入每 20 秒一次 `PING` 后完整流程通过。另一次断言失败是脚本误把 JSON 字段 `yield` 写成数据库列名 `yield_count`，修正脚本字段后通过，后端收获数据没有因此修改。

## 边界

- 本轮未修改或构建 Qt 前端。
- Qt 主界面根据 `plots` 数组动态显示；旧 `classic_farm_smoke` 把旧版 4 块地和初始数值写死，因此不能作为新规则的通过标准。
- 本轮未做并发压力测试、网络故障重试测试或公网安全测试。
