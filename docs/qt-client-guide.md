# Qt 6 基础接入指南

Qt 客户端直接连接 Go 服务器，不连接 MySQL，不使用 Protobuf。`qt/` 已实现注册登录、16 块地、六种作物的商店和仓库、农场操作、任务、好友、偷菜和邮件。界面使用默认 Qt 控件和文字。

## 环境

使用 Qt 6.11.2 Desktop MinGW 64-bit，组件为 Core、Network、WebSockets、Widgets。用 Qt Creator 打开 `qt/CMakeLists.txt`，选择该 Kit，运行 `classic_farm`。建议把构建目录放在项目外的英文路径。

```text
HTTP:      http://127.0.0.1:8080
WebSocket: ws://127.0.0.1:8080/ws
```

先启动 `server/cmd/game`，再打开 Qt。只有 Go 后端需要 `MYSQL_DSN`；即使都在本机，也需要用它指定 MySQL 地址、账号和数据库。Qt 登录框填写的是游戏账号，和 MySQL 账号无关。游戏密码为 6–20 位字母或数字。

## 操作

1. 注册并进入农场，在下拉框选择作物，填写数量后买种子。
2. 在左侧选择地块，再点击种植、施肥、收获或清理。仓库显示六种作物各自的种子和成品数量。
3. 出售使用下拉框选中的作物和数量框中的数量。购买数量为 1–100，出售为 1–200。
4. “好友”窗口内可以按游戏账号添加好友、选择好友查看农场、偷取选中成熟地块，以及填写标题和正文发邮件。
5. “邮箱”显示“系统”或玩家用户名作为发送者，点击未读邮件标记已读。

生长中的地块显示“剩余 X 秒”，每秒更新一次；到零后显示“已成熟”。客户端只根据服务端给出的 `mature_at_ms` 显示文字，实际能否收获或偷取仍由服务器判断。自己的快照也会随操作和 20 秒心跳更新，刷新保留当前作物和地块选择。

## 通信分工

- `QNetworkAccessManager`：注册、登录、注销、好友和好友邮件。
- `QWebSocket`：AUTH、农场操作、任务、原邮箱查询和 20 秒心跳。
- `FarmApiClient` 保存 token、player_id、snapshot、mails 和 friends。
- 窗口只调用 `FarmApiClient`，不直接写网络请求。

登录成功后保存 token，建立 WebSocket，并先发送：

```json
{"request_id":"auth0001","action":"AUTH","data":{"token":"登录返回值"}}
```

认证成功后发送 `GET_PLAYER_SNAPSHOT`。农场操作的成功响应携带最新 `snapshot`，客户端直接替换本地状态；配置从 WebSocket 响应读取。邮箱响应单独读取 `mails`。

## 当前兼容与差异

- 服务端 `snapshot` 里的 `seeds` 和 `crops` 是旧版胡萝卜字段，Qt 不再读取，当前界面使用 `inventory` 和 `shop` 展示六种作物。
- 地块按服务端 `plots` 数组显示，目前为 16 块。冒烟测试已更新为 50 金币、2 肥料、16 块地。
- `BUY_SEEDS`、`PLANT`、`SELL_CROP` 都发送选中的 `crop_id`。
- 服务端已经删除请求去重。断线后先重新读取快照，不要自动重发结果未知的写操作。
- 同一时间只处理一个请求。断线或 WebSocket 请求超时后回到登录页，手动重新登录。
- Session 在 Go 进程内，服务重启后客户端重新登录。

全部字段、接口、错误码和例子以 [JSON 接口契约](contracts/json-api.md) 为准。代码对应关系见 [Qt 功能升级与接口对接](qt-feature-upgrade.md)，构建后的操作测试见 [测试说明](testing.md)。
