# Qt 登录断线修复验证

日期：2026-09-15

- 复现：使用带花括号的 UUID 作为 WebSocket `request_id`，后端返回 `INVALID_ARGUMENT` 并关闭连接。
- 原因：Qt 的 `QUuid::toString()` 默认保留 `{}`，后端的请求编号规则不接受花括号。
- 修改：Qt 生成请求编号时使用 `QUuid::WithoutBraces`。
- 后端验证：临时账号的注册、HTTP 登录、WebSocket 认证和农场快照读取均返回 `OK`，快照包含 16 块地；临时账号已删除。
- 构建验证：`cmake --build %TEMP%\classic-farm-qt-vscode --target classic_farm` 成功。
