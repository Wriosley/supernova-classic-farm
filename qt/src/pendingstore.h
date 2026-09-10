#pragma once

#include "models.h"

// 把最多一条未确认写指令按 player_id 存进 QSettings（Windows 上通常是注册表）。
// 等价于演示网页里 sessionStorage 的 farm-pending:{playerId}。
// 只存 request_id、action、data。password / token 绝不能进这份本地配置。
class PendingStore
{
public:
    void save(const QString &playerId, const Command &command) const;
    Command load(const QString &playerId) const;
    void clear(const QString &playerId) const;
};
