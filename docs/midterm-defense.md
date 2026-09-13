# 中期答辩代码讲解稿

## 1. 开场与架构（约 1 分钟）

> 我的项目是经典农场。我负责后端，为了让中期版本可运行、可解释，我把它简化成一个 Go 游戏服务器和一个 MySQL 数据库。Qt 或 Vue 只通过 JSON 接口访问服务器。注册、登录和好友走 HTTP；登录后的连续游戏操作走 WebSocket。项目没有 Gate、Coordinator、分片和 Actor。

展示 `server/cmd/game/main.go`：解释 `sql.Open` 创建数据库句柄，`PingContext` 检查连接，`InitTables` 初始化表，`NewServer` 创建路由对象，`ListenAndServe` 是 Go 标准库提供的监听函数。

## 2. 数据库（约 3 分钟）

展示 `server/sql/reset_class_mid.sql` 和 `docs/architecture.md` 的 ER 图。

> 数据库有 9 张表。账号和玩家是 1:1；玩家与农田、库存、任务进度和邮件是 1:N；玩家与作物、任务及其他玩家是 N:M，所以使用库存、玩家任务和好友三张中间表。把原来的一整块 JSON 状态拆成关系表后，可以用外键表达关系，也能单独查询某个好友的 16 块农田。

依次讲关键字段：玩家表存金币等汇总数据；作物表是商店配置；库存用玩家 ID 与作物 ID 组成联合主键；农田用玩家 ID 与地块号组成联合主键；邮件有发送者和接收者；好友表保存两条方向记录。

现场 SQL：

```sql
SELECT * FROM class_mid_crops;
SELECT player_id, COUNT(*) AS plot_count FROM class_mid_plots GROUP BY player_id;
SELECT * FROM class_mid_friends;
```

## 3. 注册登录（约 2 分钟）

展示 `http.go` 的 `Handler`、`register`、`login`，再展示 `password.go`。

> `Handler` 用 Go 标准库把方法和路径绑定到处理函数。请求到来后标准库为它调用对应函数。注册先把 JSON 转成结构体，再校验格式，最后由 `createPlayer` 在一个事务中建立所有初始关系数据。密码使用课程要求的凯撒移位：字母和数字分别循环右移 3 位。登录时对输入做同样变换再比较。这个方法便于讲解，但不适合真实公网系统。

## 4. WebSocket 游戏链（约 3 分钟）

展示 `websocket.go` 和 `game.go` 的 `executeGame`、`plant`。

> HTTP 适合一次请求一次响应；进入游戏后会连续发送种植、施肥、收获等消息，所以建立一条 WebSocket 长连接。连接后先发 AUTH，服务端进入 for 循环持续读取 JSON。`action` 决定 switch 进入哪个函数，`data` 里的 `plot_id`、`crop_id` 和 `quantity` 就是具体操作参数。函数执行成功后把最新 `State` 放入 response 的 `snapshot` 返回，Qt 据此刷新整个界面。

种植重点：事务先用 `SELECT ... FOR UPDATE` 锁目标地块，检查为空；再锁仓库记录并检查种子；扣种子、写入作物和成熟时间、更新任务；`Commit` 后才回复。`FOR UPDATE` 是 SQL 要求 InnoDB 加行锁，`QueryRowContext` 是执行这条 SQL 的 Go 函数。

## 5. 好友功能（约 2 分钟）

展示 `friend.go`。

> 加好友按用户名找玩家，在一个事务里插入 A→B 和 B→A。查看农场先检查好友关系，再查询对方 ID 对应的 16 行农田。偷菜先锁对方地块，检查成熟，然后在同一事务里给自己增加完整产量和 1 金币，并把对方地块改成待清理。发邮件则向邮件表插入发送者、接收者、标题和正文。

Qt 暂无好友页面时运行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\demo-friends.ps1
```

脚本会创建两个临时账号并真实演示整个链路，最后以 B 的身份查询邮箱并打印收到的标题和正文。也可以传入 `-MailTitle "标题" -MailContent "正文"` 自定义内容。

## 6. 测试与限制（约 1 分钟）

> 我运行了 Go 单元测试、静态检查，并在本机 MySQL 上执行了双玩家完整链路。当前为了控制代码量，没有好友申请、偷菜次数限制、分页、请求去重和主动成熟推送。Session 只存在服务器内存，重启后重新登录。凯撒密码只作为课程里的简单编码示例。

不要继续引用旧版的“3 张表、4 块地、只有胡萝卜、PBKDF2、内存模式”。旧历史验证记录保留日期仅用于说明当时版本。
