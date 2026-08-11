# Tcaplus PlayerCheckpoint POC

本目录包含单条玩家 Checkpoint POC，以及纯 Tcaplus 最小运行时所需
的 PB 表定义。TcaplusDB 不是本地容器，表必须在控制台创建。

## 已固定的客户端版本

- Go module：`github.com/tencentyun/tcaplusdb-go-sdk@v0.2.3`
- SDK 内部 API：`3.55.0.000029`
- 表协议：PB

该版本支持单记录版本乐观锁和 PB 条件更新。不要改用 TDR 表定义。

## 创建测试表

1. 在腾讯云 TcaplusDB 控制台创建或选择测试集群与表格组。
2. 使用 `schema/classicfarm/v1/tcaplus/player_checkpoint.proto` 创建
   PB Generic 表。
3. 表消息名称必须保持 `PlayerCheckpoint`，主键必须为
   `player_id uint64`。
4. 如果控制台要求同时提供公共 option 文件，使用
   `schema/tcaplusservice.optionv1.proto`。
5. 记录集群接入 ID、表格组 ID、目录服务地址和访问密码。

纯 Tcaplus 模式还需使用 `runtime_tables.proto` 创建以下 PB Generic
表，表名必须与 message 名完全一致：

```text
PlayerIdCounter
AccountByName
AccountByPlayer
Session
ShardFence
MigrationProgress
PlayerOutbox
```

好友功能从阶段 2 起还需使用 `friend_tables.proto` 创建以下 PB Generic
表：

```text
FriendCodeCurrent
FriendCodeLookup
FriendRelation
FriendList
FriendLinkSaga
FriendInteraction
```

邮件与领取 Saga（04-3）还需使用 `mail_tables.proto` 创建以下 PB Generic
表，否则 `./start-servers.sh --dual-zone --tcaplus` 会在启动 MailSvr 时因
`table ... not exit` 失败：

```text
PublicMail
PrivateMail
PlayerMailboxCursor
PlayerMailState
MailSourceDedup
MailClaimSaga
```

控制台的表描述文件校验拒绝 proto3 的显式 `optional` 字段。需要表达
“缺省”时使用零值约定，并在 schema 注释中写明该零值的含义。

不要把访问密码写入本目录、`.env.example`、日志或命令历史。

## 生成 Go 类型

修改 schema 后，从本目录执行：

```bash
go run github.com/bufbuild/buf/cmd/buf@latest generate \
  --template buf.gen.yaml
```

生成文件位于：

```text
server/gen/classicfarm/v1/tcaplus/player_checkpoint.pb.go
server/gen/tcaplus/options/tcaplusservice.optionv1.pb.go
```

## 注入测试环境

在本机未跟踪的 `.env` 或 shell 中提供：

```bash
export TCAPLUS_APP_ID='<集群接入 ID>'
export TCAPLUS_ZONE_ID='<表格组 ID>'
export TCAPLUS_DIR_URL='tcp://<目录服务 IP:端口>'
export TCAPLUS_SIGNATURE='<访问密码>'
export TCAPLUS_CHECKPOINT_TABLE='PlayerCheckpoint'
export TCAPLUS_PLAYER_ID_COUNTER_TABLE='PlayerIdCounter'
export TCAPLUS_ACCOUNT_BY_NAME_TABLE='AccountByName'
export TCAPLUS_ACCOUNT_BY_PLAYER_TABLE='AccountByPlayer'
export TCAPLUS_SESSION_TABLE='Session'
export TCAPLUS_FENCE_TABLE='ShardFence'
export TCAPLUS_MIGRATION_TABLE='MigrationProgress'
export TCAPLUS_OUTBOX_TABLE='PlayerOutbox'
export TCAPLUS_POC_PLAYER_ID='<专用测试玩家 ID>'
```

Kubernetes 中必须由 Secret 注入 `TCAPLUS_SIGNATURE`，其他值可来自
ConfigMap 或 Secret。

## 执行 POC

```bash
set -a
source .env
set +a
go -C server run ./cmd/tcaplus-poc
```

成功输出只包含测试 Player ID 和 Checkpoint Revision：

```text
TCAPLUS_POC PASS ... create_load=true cas=true duplicate=true \
stale_rejected=true reload=true
```

该程序验证：

1. 不存在时 Insert；
2. Load 并取得 Tcaplus 记录版本；
3. 同时使用记录版本和 `checkpoint_revision` 执行 CAS；
4. 写入成功但客户端重试时识别为 `ALREADY_APPLIED`；
5. 拒绝旧 Token/旧 Revision 写入；
6. 再次 Load 后恢复相同 Checkpoint。

## 启动纯 Tcaplus 五进程

创建全部八张表并配置环境变量后执行：

```bash
./start-servers.sh --dual-zone --tcaplus
```

该模式拒绝 `MYSQL_DSN`，Coordinator 首次启动会幂等创建 4096 条
epoch-one `ShardFence`，Login、Zone 和 Coordinator 均不连接 MySQL。
