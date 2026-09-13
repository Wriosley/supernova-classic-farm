# Qt 新功能升级与接口对接

本文只说明当前 Qt 客户端还缺少的功能。后端接口已经实现，Qt 组员无需修改数据库或复制 Go 业务逻辑。

## 1. Qt 需要新增什么

| 优先级 | 功能 | 当前情况 | 建议界面 |
|---|---|---|---|
| 1 | 16 块地 | 主窗口能按数组显示，旧测试仍写死 4 块 | 4×4 按钮或列表 |
| 2 | 六种作物商店 | 旧按钮默认只买胡萝卜 | 作物下拉框、数量框、购买按钮 |
| 3 | 六种作物仓库 | 旧字段只显示胡萝卜 | 表格显示种子数、成品数、售价 |
| 4 | 选择作物种植和出售 | 旧请求不发送 crop_id | 将选中作物 ID 放进 JSON |
| 5 | 好友列表与添加好友 | 尚无界面 | `FriendDialog` |
| 6 | 查看好友农场与偷菜 | 尚无界面 | `FriendFarmDialog` |
| 7 | 给好友发邮件 | 尚无界面 | 标题输入框、正文输入框、发送按钮 |
| 8 | 邮件发送人 | 旧邮件模型没有 sender_id | 邮箱列表增加发送者 ID |

推荐先完成多作物数据解析，再开发好友页面。好友接口都是普通 HTTP，不影响已经存在的农场 WebSocket。

## 2. 新增 C++ 数据结构

在 `qt/src/models.h` 中增加：

```cpp
struct CropInfo {
    int cropId = 0;
    QString name;
    int seedPrice = 0;
    int salePrice = 0;
    int matureSeconds = 0;
    int yieldCount = 0;
};

struct InventoryItem {
    int cropId = 0;
    QString name;
    int seedCount = 0;
    int cropCount = 0;
};

struct FriendInfo {
    QString playerId;
    QString username;
};
```

给现有 `Plot` 增加：

```cpp
int cropId = 0;
QString cropName;
```

给现有 `Mail` 增加：

```cpp
QString senderId; // 系统欢迎邮件可能为空
```

给 `Snapshot` 增加：

```cpp
QVector<CropInfo> shop;
QVector<InventoryItem> inventory;
```

所有数据库 BIGINT ID 在 JSON 中都是字符串，Qt 使用 `QString`，不要先转成 `double`。

## 3. 解析商店、仓库和地块

服务端快照示例：

```json
{
  "coins":50,
  "fertilizer":2,
  "inventory":[
    {"crop_id":1,"crop_name":"胡萝卜","seed_count":0,"crop_count":0}
  ],
  "shop":[
    {"crop_id":1,"crop_name":"胡萝卜","seed_price":2,"sale_price":5,"mature_seconds":30,"yield":3}
  ],
  "plots":[
    {"plot_id":1,"crop_id":0,"crop_name":"","status":"EMPTY","planted_at_ms":0,"mature_at_ms":0,"fertilized":false}
  ]
}
```

Qt 解析数组的写法：

```cpp
for (const QJsonValue &value : object.value("inventory").toArray()) {
    const QJsonObject item = value.toObject();
    InventoryItem data;
    data.cropId = item.value("crop_id").toInt();
    data.name = item.value("crop_name").toString();
    data.seedCount = item.value("seed_count").toInt();
    data.cropCount = item.value("crop_count").toInt();
    snapshot.inventory.append(data);
}
```

`shop` 使用相同方式解析。每次收到完整快照前先清空旧数组，避免重复追加。

## 4. 多作物游戏请求

这些请求继续走现有 `QWebSocket` 和 `performWrite()`：

```cpp
api->performWrite("BUY_SEEDS", {
    {"crop_id", selectedCropId},
    {"quantity", quantity}
});

api->performWrite("PLANT", {
    {"plot_id", selectedPlotId},
    {"crop_id", selectedCropId}
});

api->performWrite("SELL_CROP", {
    {"crop_id", selectedCropId},
    {"quantity", quantity}
});
```

施肥、收获和清理只需要 `plot_id`。客户端不要提交价格、产量、金币、成熟时间和玩家 ID，服务器从数据库计算。

## 5. 好友 HTTP 接口

所有好友请求都要添加：

```text
Authorization: Bearer 登录返回的 token
```

建议在 `FarmApiClient` 增加：

```cpp
void addFriend(const QString &username);
void getFriends();
void getFriendFarm(const QString &friendId);
void stealFromFriend(const QString &friendId, int plotId);
void sendFriendMail(const QString &friendId,
                    const QString &title,
                    const QString &content);
```

建议增加信号：

```cpp
void friendsUpdated();
void friendFarmLoaded(const QString &friendId, const QVector<Plot> &plots);
void friendMailSent(const QString &friendId);
```

可以写一个共用的认证请求函数：

```cpp
QNetworkRequest FarmApiClient::authorizedRequest(const QString &path)
{
    QNetworkRequest request(QUrl(baseHttpUrl + path));
    request.setHeader(QNetworkRequest::ContentTypeHeader, "application/json");
    request.setRawHeader("Authorization", "Bearer " + token.toUtf8());
    return request;
}
```

### 5.1 添加好友

```http
POST /api/friends/add
```

```json
{"username":"student_b"}
```

成功：

```json
{"type":"response","code":"OK","friend_id":"26","server_time_ms":1800000000000}
```

禁止添加自己；重复添加返回 `ALREADY_FRIENDS`；用户名不存在返回 `FRIEND_NOT_FOUND`。

### 5.2 获取好友列表

```http
GET /api/friends
```

```json
{
  "code":"OK",
  "friends":[{"player_id":"26","username":"student_b"}]
}
```

没有好友时 `friends` 可能不存在或为空，Qt 都应当按空数组处理。

### 5.3 查看好友农田

```http
GET /api/friends/26/farm
```

成功响应含 `friend_id` 和 `plots`。`plots` 的字段与自己的农田相同，当前固定返回 16 项。不是好友返回 `FRIEND_NOT_FOUND`。

### 5.4 偷菜

```http
POST /api/friends/26/steal
```

```json
{"plot_id":1}
```

成功响应含偷取者自己的最新 `snapshot`。Qt 应刷新自己的金币和仓库，然后重新查询好友农田。作物未成熟返回 `NOT_MATURE`；空地、已收获或已被偷返回 `INVALID_PLOT_STATE`。

当前规则：获得该作物完整产量和 1 金币，好友地块变为 `NEED_CLEANUP`。客户端只显示结果，不自行增加数量。

### 5.5 给好友发送自定义邮件

```http
POST /api/friends/26/mail
```

```json
{"title":"你好","content":"我来参观你的农场了"}
```

标题不能为空且最多 100 字节，正文不能为空且最多 1000 字节。成功响应含 `friend_id`，`mails` 只含服务端插入后按 B 的 ID 读回的新邮件，可用于联调确认，不会返回 B 的其他邮件。正式 Qt 只需提示发送成功；B 打开邮箱时仍通过现有 `GET_MAILBOX` 获得邮件。

## 6. Qt 好友页面建议

`FriendDialog`：

- 顶部用户名输入框和“添加好友”按钮。
- 中间 `QListWidget` 显示用户名，并把 `player_id` 放在 `Qt::UserRole`。
- 底部“查看农场”和“发邮件”按钮。

`FriendFarmDialog`：

- 显示好友用户名或 ID。
- 用 4×4 按钮或列表显示 16 块地。
- 仅当状态为 `MATURE` 时启用“偷菜”。
- 偷菜成功后使用响应 `snapshot` 刷新自己的主窗口，再重新请求好友农田。

发邮件可使用 `QDialog`，包含 `QLineEdit` 标题、`QPlainTextEdit` 正文和发送按钮。前端先检查非空，最终长度和好友关系仍由服务端校验。

## 7. HTTP POST 示例

```cpp
void FarmApiClient::sendFriendMail(const QString &friendId,
                                   const QString &title,
                                   const QString &content)
{
    QNetworkRequest request = authorizedRequest(
        "/api/friends/" + friendId + "/mail");
    QJsonObject body{{"title", title}, {"content", content}};
    QNetworkReply *reply = network.post(
        request, QJsonDocument(body).toJson(QJsonDocument::Compact));

    connect(reply, &QNetworkReply::finished, this, [this, reply, friendId] {
        const QJsonObject object =
            QJsonDocument::fromJson(reply->readAll()).object();
        reply->deleteLater();
        if (object.value("code").toString() != "OK") {
            emit requestFailed(object.value("code").toString(),
                               object.value("message").toString());
            return;
        }
        emit friendMailSent(friendId);
    });
}
```

GET 请求使用 `network.get(authorizedRequest(path))`，解析流程相同。

## 8. 错误码处理

| code | Qt 提示或动作 |
|---|---|
| `INVALID_ARGUMENT` | 检查输入、数量、地块号和邮件长度 |
| `INVALID_CREDENTIALS` | 账号或密码错误 |
| `UNAUTHENTICATED` | 清空 token，返回登录页 |
| `ACCOUNT_EXISTS` | 用户名已存在 |
| `NOT_ENOUGH_COINS` | 金币不足 |
| `NOT_ENOUGH_ITEMS` | 种子、作物或肥料不足 |
| `INVALID_PLOT_STATE` | 刷新农田并提示状态已改变 |
| `NOT_MATURE` | 显示剩余成熟时间 |
| `FRIEND_NOT_FOUND` | 好友不存在或双方不是好友 |
| `ALREADY_FRIENDS` | 已经是好友 |
| `SERVICE_UNAVAILABLE` | 提示服务错误并重新查询状态 |

不要根据中文 `message` 编写程序分支，应判断稳定的 `code`。

## 9. 推荐开发顺序与验收

1. 更新 `models.h`，解析 `shop`、`inventory`、地块作物和邮件发送者。
2. 更新主界面，展示 16 块地和六种作物库存。
3. 给购买、种植、出售请求增加 `crop_id`。
4. 新增好友列表与添加好友。
5. 新增好友农场与偷菜。
6. 新增自定义邮件窗口。
7. 修改 `smoketest.cpp` 的旧初始值，增加多作物解析断言。
8. 增加一条好友 Qt 冒烟：A/B 注册、A 加 B、读取 16 块地、A 发信、B 查询邮箱。

联调时先启动后端，再在 Qt 日志中打印请求路径、HTTP 状态、`code` 和 `request_id`；不要打印 token 或密码。每完成一个页面，都用真实 MySQL 账号退出重登一次，确认数据不是只保存在 Qt 内存里。
