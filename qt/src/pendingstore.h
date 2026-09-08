#pragma once

#include "models.h"

// 按玩家保存最多一条未确认写指令，供断线后用原 request_id 重试。
class PendingStore
{
public:
    void save(const QString &playerId, const Command &command) const;
    Command load(const QString &playerId) const;
    void clear(const QString &playerId) const;
};
