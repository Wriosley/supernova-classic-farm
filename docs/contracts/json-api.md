# JSON 接口契约

服务地址默认是 `http://127.0.0.1:8080`，WebSocket 地址是 `ws://127.0.0.1:8080/ws`。HTTP 和 WebSocket 都传 UTF-8 JSON。Qt 只连接服务端，不直接连接 MySQL。

## HTTP 接口

| 方法与路径 | 请求 | 成功响应中的主要字段 |
|---|---|---|
| `GET /healthz` | 无 | `code` |
| `GET /api/config` | 无 | `config` |
| `POST /api/register` | `username,password` | `player_id`，HTTP 201 |
| `POST /api/login` | `username,password` | `player_id,token` |
| `POST /api/logout` | Bearer token | `code` |
| `GET/POST /api/mailbox` | Bearer token | `mails`，玩家邮件含 `sender_id,sender_username`；POST 供 PowerShell 演示脚本使用 |
| `POST /api/friends/add` | Bearer token；`username` | `friend_id` |
| `GET /api/friends` | Bearer token | `friends` |
| `GET /api/friends/{id}/farm` | Bearer token | `friend_id,plots` |
| `POST /api/friends/{id}/steal` | Bearer token；`plot_id` | `snapshot` |
| `POST /api/friends/{id}/mail` | Bearer token；`title,content` | `friend_id,mails`；mails 只含写入后读回的新邮件 |

注册例子：

```json
{"username":"student_a","password":"Farm1234"}
```

账号为 3–32 位小写字母、数字或下划线，以字母开头；密码为 6–20 位字母或数字。登录后保存 `token`。好友 HTTP 请求带请求头：`Authorization: Bearer <token>`。

好友邮件标题和正文由客户端自定义；标题为 1–100 个字节，正文为 1–1000 个字节。发送成功后，B 可以通过 WebSocket `GET_MAILBOX` 查询邮件。玩家邮件返回发送者 ID 和用户名，系统邮件不含这两个字段。

## WebSocket 接口

连接后的第一条消息必须认证：

```json
{"request_id":"auth0001","action":"AUTH","data":{"token":"登录返回的 token"}}
```

之后每条命令都是 `{"request_id":"plant001","action":"PLANT","data":{"plot_id":1,"crop_id":1}}`。

| action | data | 说明 |
|---|---|---|
| `PING` / `GET_PLAYER_SNAPSHOT` | `{}` | 读取当前快照 |
| `GET_SHOP` | `{}` | 读取含 6 种作物的快照 |
| `BUY_SEEDS` | `crop_id,quantity` | 买种子；省略 crop_id 时为胡萝卜 |
| `BUY_FERTILIZER` | `quantity` | 买肥料 |
| `PLANT` | `plot_id,crop_id` | 在 1–16 号地种植 |
| `APPLY_FERTILIZER` | `plot_id` | 成熟时间减少 10 秒 |
| `HARVEST` / `CLEAN_PLOT` | `plot_id` | 收获 / 清理地块 |
| `SELL_CROP` | `crop_id,quantity` | 出售仓库作物 |
| `CLAIM_CHAPTER_REWARD` | `{}` | 完成所有任务后领奖 |
| `GET_MAILBOX` | `{}` | 查询自己的邮件 |
| `READ_MAIL` | `mail_id` | 标记自己的邮件已读 |

农场命令成功响应含 `type,code,request_id,action,snapshot,config,server_time_ms`；邮箱命令返回 `mails`，AUTH 不携带快照。`snapshot` 含 `coins,fertilizer,chapter,inventory,shop,plots,tasks`。为兼容旧客户端，胡萝卜数量还会同时写入 `seeds,crops`。

购买数量为 1–100，出售数量为 1–200。没有邮件或好友时，对应数组可能省略，客户端按空列表处理。玩家 ID、好友 ID、邮件 ID 都使用 JSON 字符串；地块号和作物号为整数。

## 作物编号

| ID | 名称 | 种子价 | 售价 | 成熟秒 | 产量 |
|---:|---|---:|---:|---:|---:|
| 1 | 胡萝卜 | 2 | 5 | 30 | 3 |
| 2 | 玉米 | 3 | 7 | 35 | 3 |
| 3 | 土豆 | 4 | 9 | 40 | 4 |
| 4 | 番茄 | 5 | 11 | 45 | 4 |
| 5 | 草莓 | 6 | 14 | 50 | 5 |
| 6 | 南瓜 | 8 | 18 | 60 | 5 |

常见错误码：`INVALID_ARGUMENT`、`INVALID_CREDENTIALS`、`UNAUTHENTICATED`、`ACCOUNT_EXISTS`、`INSUFFICIENT_COINS`、`INSUFFICIENT_ITEMS`、`PLOT_STATE_CONFLICT`、`CROP_NOT_MATURE`、`CHAPTER_NOT_CLAIMABLE`、`MAIL_NOT_FOUND`、`FRIEND_NOT_FOUND`、`ALREADY_FRIENDS`、`SERVICE_UNAVAILABLE`。这些名称对应当前 `server/internal/game/model.go` 和 `http.go`。
