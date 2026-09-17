# Qt 新功能升级与接口对接

本文说明 2026-09-14 补齐的 Qt 功能与接口对应关系。Qt 使用服务端数据，不修改数据库或复制 Go 业务逻辑。

## 1. 已加入的功能

| 序号 | 功能 | 当前情况 | 界面 |
|---|---|---|---|
| 1 | 16 块地 | 按数组显示，刷新保留选择 | `QListWidget` |
| 2 | 六种作物商店 | 显示名称、价格、成熟秒数、产量 | 作物下拉框、数量框、购买按钮 |
| 3 | 六种作物仓库 | 显示各作物数量和售价 | 文字列表 |
| 4 | 选择作物种植和出售 | 请求携带选中的 crop_id | 共用作物下拉框 |
| 5 | 好友列表与添加好友 | 已加入 | `FriendDialog` |
| 6 | 查看好友农场与偷菜 | 已加入 | 同一个 `FriendDialog` 中的地块列表 |
| 7 | 给好友发邮件 | 已加入 | 同一窗口的标题、正文和发送按钮 |
| 8 | 邮件发送人 | 已解析 sender_id 和 sender_username | 邮箱显示“系统”或发送者用户名 |

好友接口使用普通 HTTP，原有农场操作继续使用 WebSocket。沿用简单的成员变量、循环和信号槽，没有额外的页面框架。

## 2. 新增 C++ 数据结构

`qt/src/models.h` 中的数据结构：

```cpp
struct Crop {
    int id = 0;
    QString name;
    int seedPrice = 0;
    int salePrice = 0;
    int seconds = 0;
    int yield = 0;
};

struct Bag {
    int id = 0;
    QString name;
    int seeds = 0;
    int crops = 0;
};

struct Friend {
    QString id;
    QString name;
};
```

`Plot` 中的作物字段：

```cpp
int cropId = 0;
QString cropName;
```

`Mail` 中的发送者字段：

```cpp
QString senderId; // 系统欢迎邮件可能为空
QString senderName;
```

`Farm` 中的数组：

```cpp
QVector<Crop> shop;
QVector<Bag> inventory;
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
    Bag data;
    data.id = item.value("crop_id").toInt();
    data.name = item.value("crop_name").toString();
    data.seeds = item.value("seed_count").toInt();
    data.crops = item.value("crop_count").toInt();
    snapshot.inventory.append(data);
}
```

`shop` 使用相同方式解析。`readFarm()` 每次创建一个新的 `Farm` 并返回，再整体赋给客户端，避免重复追加。

## 4. 多作物游戏请求

这些请求继续走现有 `QWebSocket` 和 `game()`：

```cpp
api->game("BUY_SEEDS", {
    {"crop_id", selectedCropId},
    {"quantity", quantity}
});

api->game("PLANT", {
    {"plot_id", selectedPlotId},
    {"crop_id", selectedCropId}
});

api->game("SELL_CROP", {
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

`FarmApiClient` 中的好友方法：

```cpp
void add(QString username);
void getList();
void visit(QString friendId);
void steal(QString friendId, int plotId);
void send(QString friendId, QString title, QString content);
```

窗口接收以下信号：

```cpp
void listChanged();
void visited(QString friendId, QVector<Plot> plots);
void sent();
```

每个方法直接创建 `QNetworkRequest`、添加请求头，并在自己的完成回调中读取结果。这里保留少量重复代码，方便从一个函数顺着看完整个请求。

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

成功响应含偷取者自己的最新 `snapshot`。Qt 刷新自己的金币和仓库，然后重新查询好友农田。作物未成熟返回 `CROP_NOT_MATURE`；空地、已收获或已被偷返回 `PLOT_STATE_CONFLICT`。

当前规则：获得该作物完整产量和 1 金币，好友地块变为 `NEED_CLEANUP`。客户端只显示结果，不自行增加数量。

### 5.5 给好友发送自定义邮件

```http
POST /api/friends/26/mail
```

```json
{"title":"你好","content":"我来参观你的农场了"}
```

标题不能为空且最多 100 字节，正文不能为空且最多 1000 字节。成功响应含 `friend_id`，`mails` 只含服务端插入后按 B 的 ID 读回的新邮件，可用于联调确认，不会返回 B 的其他邮件。正式 Qt 只需提示发送成功；B 打开邮箱时仍通过现有 `GET_MAILBOX` 获得邮件。

## 6. Qt 好友页面

`FriendDialog`：

- 顶部用户名输入框和“添加好友”按钮。
- 中间 `QListWidget` 显示用户名，并把 `player_id` 放在 `Qt::UserRole`。
- 右侧列表显示该好友的 16 块地，点击“查看/刷新好友农场”查询。
- 仅当状态为 `MATURE` 时启用“偷菜”。
- 偷菜成功后使用响应 `snapshot` 刷新自己的主窗口，再重新请求好友农田。

同一窗口下方放 `QLineEdit` 标题、`QPlainTextEdit` 正文和发送按钮。发送前检查非空和 UTF-8 字节数，最终仍由服务端校验。发送成功只清空输入框并提示，不用响应中的 `mails` 覆盖自己的收件箱。


GET 请求直接使用 `http.get(request)`，解析流程相同。HTTP 超时后显示错误；WebSocket 断线或超时后回登录页，不自动重发写操作。

## 8. 错误码处理

| code | Qt 提示或动作 |
|---|---|
| `INVALID_ARGUMENT` | 检查输入、数量、地块号和邮件长度 |
| `INVALID_CREDENTIALS` | 账号或密码错误 |
| `UNAUTHENTICATED` | 清空 token，返回登录页 |
| `ACCOUNT_EXISTS` | 用户名已存在 |
| `INSUFFICIENT_COINS` | 金币不足 |
| `INSUFFICIENT_ITEMS` | 种子、作物或肥料不足 |
| `PLOT_STATE_CONFLICT` | 提示地块状态不允许操作，可手动刷新 |
| `CROP_NOT_MATURE` | 提示作物尚未成熟 |
| `CHAPTER_NOT_CLAIMABLE` | 提示先完成任务 |
| `MAIL_NOT_FOUND` | 邮件不存在 |
| `FRIEND_NOT_FOUND` | 好友不存在或双方不是好友 |
| `ALREADY_FRIENDS` | 已经是好友 |
| `SERVICE_UNAVAILABLE` | 提示服务错误，可手动刷新状态 |

不要根据中文 `message` 编写程序分支，应判断稳定的 `code`。

## 9. 代码阅读与验收

1. `models.h`：读取快照、库存和邮件。
2. `farmwindow.cpp`：创建主窗口控件，按钮中发送 crop_id、plot_id 和 quantity。
3. `farmapiclient.cpp`：HTTP/WS 请求及返回数据。
4. `frienddialog.cpp`：添加好友、查看地块、偷菜和发信。
5. `mailboxdialog.cpp`：显示发送者、正文和已读状态。
6. 构建 `classic_farm`，再使用两个新玩家手动检查多作物、好友邮件和退出重登。

构建方法和手动检查范围见 [测试说明](testing.md)。
