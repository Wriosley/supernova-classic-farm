# C++/Qt 6 前端接入指南

本文面向负责重写前端的组员。仓库中的实现位于 `qt/`。Qt 客户端直接连接 Go 服务，复用现有 UTF-8 JSON 合同，不使用 Protobuf，也不连接 MySQL。

## 1. 环境和模块

建议使用 Qt 6、C++17，并启用 Network 与 WebSockets：

```cmake
cmake_minimum_required(VERSION 3.21)
project(ClassicFarmQt LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_AUTOMOC ON)
find_package(Qt6 REQUIRED COMPONENTS Core Network WebSockets Widgets)
add_executable(classic_farm main.cpp farmapiclient.cpp farmapiclient.h)
target_link_libraries(classic_farm PRIVATE
    Qt6::Core Qt6::Network Qt6::WebSockets Qt6::Widgets)
```

本机默认地址：

```text
HTTP:      http://127.0.0.1:8080
WebSocket: ws://127.0.0.1:8080/ws
```

局域网联调时，后端设置 `GAME_ADDR=0.0.0.0:8080`，Qt 把 `127.0.0.1` 换成服务器电脑的局域网 IP。

## 2. 推荐客户端结构

```text
FarmApiClient
  ├─ QNetworkAccessManager：注册、登录、注销、配置
  ├─ QWebSocket：AUTH、游戏指令、邮箱、心跳
  ├─ QHash<QString, PendingRequest>：request_id 对应回调
  └─ 当前 token/player_id/snapshot/config/mails

LoginWindow / FarmWindow / MailboxDialog
  └─ 只调用 FarmApiClient，不直接访问网络和数据库
```

建议先写一个 `FarmApiClient : public QObject` 封装网络，再让各窗口连接它的 signal。这样 UI 不需要知道 HTTP 状态码、WebSocket 重连和 JSON 字段细节。

## 3. 统一响应格式

HTTP 和 WebSocket 都返回：

```json
{
  "type": "response",
  "request_id": "plant-0001",
  "action": "PLANT",
  "code": "OK",
  "message": "",
  "server_time_ms": 1800000000000
}
```

- `type` 固定为 `response`。
- `code == "OK"` 才是成功。不要通过中文 `message` 判断逻辑。
- WebSocket 响应会回传 `request_id` 和 `action`。
- `server_time_ms` 是 Unix 毫秒，用于修正本机倒计时。
- 不同接口还会带 `player_id`、`token`、`snapshot`、`config` 或 `mails`。

## 4. HTTP 接口

所有 POST 使用 `Content-Type: application/json`。注册和登录请求体最多 4096 字节，不允许合同之外的字段。

| 方法 | 路径 | 请求 | 成功 |
|---|---|---|---|
| GET | `/healthz` | 无 | HTTP 200，`code=OK` |
| GET | `/api/config` | 无 | HTTP 200，返回 `config` |
| POST | `/api/register` | `username,password` | HTTP 201，返回 `player_id` |
| POST | `/api/login` | `username,password` | HTTP 200，返回 `player_id,token` |
| POST | `/api/logout` | Header `Authorization: Bearer TOKEN` | HTTP 200 |

用户名必须匹配 `[a-z][a-z0-9_]{2,31}`。密码必须是合法 UTF-8，长度为 8–128 字节，服务端不会自动裁剪空格。

注册请求：

```json
{"username":"student_a","password":"example-password"}
```

注册成功后还要调用登录；注册响应不会返回 token。

登录成功：

```json
{
  "type":"response",
  "code":"OK",
  "server_time_ms":1800000000000,
  "player_id":"32位十六进制ID",
  "token":"64位十六进制凭证"
}
```

HTTP 常见失败：`INVALID_ARGUMENT`(400)、`INVALID_CREDENTIALS`(401)、`UNAUTHENTICATED`(401)、`ACCOUNT_EXISTS`(409)、`SERVER_BUSY`(429)、`SERVICE_UNAVAILABLE`(503)。

### Qt HTTP POST 示例

```cpp
void FarmApiClient::login(const QString &username, const QString &password)
{
    QNetworkRequest request(QUrl(baseHttpUrl + "/api/login"));
    request.setHeader(QNetworkRequest::ContentTypeHeader, "application/json");

    QJsonObject body{{"username", username}, {"password", password}};
    QNetworkReply *reply = network.post(request, QJsonDocument(body).toJson(QJsonDocument::Compact));
    connect(reply, &QNetworkReply::finished, this, [this, reply] {
        const int status = reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt();
        const QJsonObject obj = QJsonDocument::fromJson(reply->readAll()).object();
        reply->deleteLater();
        if (status != 200 || obj.value("code").toString() != "OK") {
            emit requestFailed(obj.value("code").toString(), obj.value("message").toString());
            return;
        }
        token = obj.value("token").toString();
        playerId = obj.value("player_id").toString();
        openGameSocket();
    });
}
```

token 只保存在进程内存。不要写进日志、URL、Git 配置或长期明文文件。

## 5. WebSocket 建连与 AUTH

连接成功后 5 秒内必须发送 AUTH。每条消息都是完整 JSON 文本，不发送二进制帧或额外长度头。

```json
{"request_id":"auth-0001","action":"AUTH","data":{"token":"登录token"}}
```

AUTH 成功：

```json
{"type":"response","request_id":"auth-0001","action":"AUTH","code":"OK","server_time_ms":1800000000000,"player_id":"玩家ID"}
```

`request_id` 长度 8–64，只能含字母、数字、冒号、下划线和连字符。Qt 可用：

```cpp
QString FarmApiClient::newRequestId() const
{
    return QUuid::createUuid().toString(QUuid::WithoutBraces);
}
```

### QWebSocket 示例

```cpp
void FarmApiClient::openGameSocket()
{
    connect(&socket, &QWebSocket::connected, this, [this] {
        sendCommand("AUTH", QJsonObject{{"token", token}});
    });
    connect(&socket, &QWebSocket::textMessageReceived,
            this, &FarmApiClient::handleTextMessage);
    connect(&socket, &QWebSocket::disconnected,
            this, &FarmApiClient::handleDisconnected);
    socket.open(QUrl(baseWsUrl + "/ws"));
}

QString FarmApiClient::sendCommand(const QString &action, const QJsonObject &data)
{
    const QString id = newRequestId();
    const QJsonObject command{{"request_id", id}, {"action", action}, {"data", data}};
    socket.sendTextMessage(QString::fromUtf8(
        QJsonDocument(command).toJson(QJsonDocument::Compact)));
    return id;
}
```

AUTH 成功后立即发 `GET_PLAYER_SNAPSHOT`。保持 20 秒一次 `PING`，服务端 60 秒收不到任何消息会断开。

```cpp
heartbeat.setInterval(20'000);
connect(&heartbeat, &QTimer::timeout, this, [this] {
    sendCommand("PING", QJsonObject{});
});
```

## 6. 全部 WebSocket action

| action | data | 成功响应 |
|---|---|---|
| `AUTH` | `token` 字符串 | `player_id` |
| `PING` | `{}` | `snapshot,config` |
| `GET_PLAYER_SNAPSHOT` | `{}` | `snapshot,config` |
| `GET_SHOP` | `{}` | `snapshot,config` |
| `BUY_SEEDS` | `quantity`，1–100 整数 | `snapshot,config` |
| `BUY_FERTILIZER` | `quantity`，1–100 整数 | `snapshot,config` |
| `PLANT` | `plot_id`，1–4 | `snapshot,config` |
| `APPLY_FERTILIZER` | `plot_id`，1–4 | `snapshot,config` |
| `HARVEST` | `plot_id`，1–4 | `snapshot,config` |
| `CLEAN_PLOT` | `plot_id`，1–4 | `snapshot,config` |
| `SELL_CROP` | `quantity`，1–200 整数 | `snapshot,config` |
| `CLAIM_CHAPTER_REWARD` | `{}` | `snapshot,config` |
| `GET_MAILBOX` | `{}` | `mails` |
| `READ_MAIL` | `mail_id` 十进制字符串 | `mails`，目标已读 |

示例：

```json
{"request_id":"plant-0001","action":"PLANT","data":{"plot_id":1}}
```

客户端永远不提交金币、价格、成熟时间、产量或 `player_id`；这些值由服务端计算。

## 7. 数据模型

### Snapshot

| 字段 | Qt 类型 | 说明 |
|---|---|---|
| `player_id` | QString | 玩家 ID |
| `state_version` | QString → `toULongLong()` | 状态版本，JSON 中是字符串 |
| `coins,seeds,fertilizer,crops` | int | 金币和背包数量 |
| `plots` | QVector&lt;Plot&gt; | 固定四块地 |
| `chapter` | int | 当前章节 |
| `tasks` | QVector&lt;Task&gt; | 章节任务 |

只接受版本大于或等于当前版本的 snapshot，防止较晚到达的旧响应覆盖新界面。同版本也需要更新时间显示，因为成熟状态随时间变化。

### Plot

`plot_id` 为 1–4；`status` 是 `EMPTY`、`GROWING`、`MATURE`、`NEED_CLEANUP`；时间为 Unix 毫秒；`fertilized` 表示本株是否使用过肥料。

### Task

包含 `action`、中文 `label`、`current`、`target`。当所有 `current >= target` 时显示领奖按钮。

### Config

当前值：种子 2、肥料 2、胡萝卜售价 5、成长 100 秒、肥料缩短 30 秒、收获 3、仓库容量 200。Qt 应读取响应字段，不把这些值当作可提交参数。

### Mail

```json
{"mail_id":"1","title":"欢迎来到经典农场","content":"欢迎来到经典农场！快去种下你的第一颗胡萝卜吧。","is_read":false,"created_at_ms":1800000000000}
```

`mail_id` 必须保留为 QString，需要数值时调用 `toULongLong()`；不要先经过 double。每次打开邮箱发送 `GET_MAILBOX`，点击未读邮件发送 `READ_MAIL`，成功后用返回的整个 `mails` 替换本地列表。

### 解析响应

```cpp
void FarmApiClient::handleTextMessage(const QString &text)
{
    QJsonParseError parseError;
    const QJsonDocument doc = QJsonDocument::fromJson(text.toUtf8(), &parseError);
    if (parseError.error != QJsonParseError::NoError || !doc.isObject()) {
        socket.close();
        return;
    }
    const QJsonObject obj = doc.object();
    const QString code = obj.value("code").toString();
    const QString id = obj.value("request_id").toString();
    const QString action = obj.value("action").toString();
    if (code == "UNAUTHENTICATED") {
        heartbeat.stop();
        emit loginRequired();
        return;
    }
    if (code != "OK") {
        emit requestFailed(code, obj.value("message").toString());
        return;
    }
    if (obj.contains("snapshot")) emit snapshotReceived(obj.value("snapshot").toObject());
    if (obj.contains("config")) emit configReceived(obj.value("config").toObject());
    if (obj.contains("mails")) emit mailsReceived(obj.value("mails").toArray());
    emit requestFinished(id, action);
}
```

## 8. 错误处理

| code | UI 处理 |
|---|---|
| `INVALID_ARGUMENT` | 检查客户端字段；不要原样重复非法请求 |
| `INVALID_CREDENTIALS` | 提示账号或密码错误 |
| `UNAUTHENTICATED` | 清空 token，回登录页 |
| `ACCOUNT_EXISTS` | 注册页提示换用户名 |
| `SERVER_BUSY` | 稍后重新注册/登录 |
| `INSUFFICIENT_COINS` | 刷新/显示金币不足 |
| `INSUFFICIENT_ITEMS` | 显示物品不足 |
| `PLOT_STATE_CONFLICT` | 使用最新 snapshot 更新地块 |
| `CROP_NOT_MATURE` | 继续显示倒计时 |
| `WAREHOUSE_FULL` | 提示先出售物品 |
| `CHAPTER_NOT_CLAIMABLE` | 提示先完成任务 |
| `REQUEST_ID_CONFLICT` | 客户端错误：同编号被用于不同内容 |
| `MAIL_NOT_FOUND` | 刷新邮箱或提示邮件不存在 |
| `SERVICE_UNAVAILABLE` | 结果可能未知，保留原请求 |

## 9. 断线和未确认请求

写操作包括购买、种植、施肥、收获、清理、出售和领奖。发送前保存完整 JSON 及 request_id；收到明确成功或业务失败后删除。若超时、断线或收到 `SERVICE_UNAVAILABLE`，重新登录/连接后用同一个 request_id 和完全相同的数据重发，服务端去重。

Qt 可用 `QSettings` 或仅当前会话文件保存一条未确认指令，但不要把 token 和密码一起持久化。读取操作、PING、邮箱查询不需要持久化。当前 `READ_MAIL` 也不进入农场去重记录，不建议自动重试。

## 10. 从 Vue 重构到 Qt 的顺序

1. 创建空 Qt Widgets 工程和 `FarmApiClient`。
2. 联调 `/healthz`、注册、登录，确认拿到 token。
3. 联调 WebSocket、AUTH、`GET_PLAYER_SNAPSHOT`，先用日志打印 JSON。
4. 建立 C++ Snapshot/Plot/Task/Config/Mail 数据结构和解析函数。
5. 完成四块地和背包的只读显示。
6. 依次接购买、种植、施肥、收获、清理、出售和领奖按钮。
7. 增加邮箱实时查询与已读。
8. 增加心跳、服务器时间修正、断线重连和未确认请求恢复。
9. 对照 [JSON 合同](contracts/json-api.md) 联调错误码，不通过中文 message 写业务分支。

前后端协作时，先在 JSON 合同中确认字段，再分别修改 Go 和 Qt。抓取一条真实请求/响应作为联调样例；客户端不需要理解数据库表，也不需要复制 Go 内部结构。
