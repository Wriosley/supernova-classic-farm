# Qt 6 基础接入指南

Qt 客户端直接连接 Go 服务器，不连接 MySQL，不使用 Protobuf。当前项目已经包含 `qt/` 客户端，它实现了注册登录、WebSocket AUTH、基础胡萝卜操作和邮箱。

## 环境

使用 Qt 6.11.2 Desktop MinGW 64-bit，组件为 Core、Network、WebSockets、Widgets。用 Qt Creator 打开 `qt/CMakeLists.txt`，构建目录使用纯英文路径。

```text
HTTP:      http://127.0.0.1:8080
WebSocket: ws://127.0.0.1:8080/ws
```

## 通信分工

- `QNetworkAccessManager`：注册、登录、注销、配置、好友和好友邮件。
- `QWebSocket`：AUTH、农场操作、任务、原邮箱查询和 20 秒心跳。
- `FarmApiClient` 保存 token、player_id、snapshot、mails 和 friends。
- 窗口只调用 `FarmApiClient`，不直接写网络请求。

登录成功后保存 token，建立 WebSocket，并先发送：

```json
{"request_id":"auth0001","action":"AUTH","data":{"token":"登录返回值"}}
```

认证成功后发送 `GET_PLAYER_SNAPSHOT`。所有游戏操作的成功响应都携带最新 `snapshot`，客户端直接替换本地状态。

## 当前兼容与差异

- 现有 `Snapshot` 的 `seeds` 和 `crops` 仍代表胡萝卜，旧按钮可以继续使用。
- 新版还返回 `inventory` 和 `shop` 数组，新 Qt 应以它们展示 6 种作物。
- 地块由 4 块改为 16 块，主窗口已按数组循环显示，但旧冒烟测试写死了 4 块地，需要更新断言。
- `BUY_SEEDS`、`PLANT`、`SELL_CROP` 新增可选 `crop_id`；省略时仍表示胡萝卜。
- 服务端已经删除请求去重。断线后先重新读取快照，不要自动重发结果未知的写操作。
- Session 在 Go 进程内，服务重启后客户端重新登录。

全部字段、接口、错误码和例子以 [JSON 接口契约](contracts/json-api.md) 为准。要开发好友与多作物 UI，请继续阅读 [Qt 功能升级与接口对接](qt-feature-upgrade.md)。
