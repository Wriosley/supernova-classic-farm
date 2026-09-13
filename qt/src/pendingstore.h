#pragma once

#include "models.h"

// 只保存未确认的操作，不保存账号密码或 token。
class PendingStore
{
public:

    void save(const QString &playerId, const Command &command) const;

    Command load(const QString &playerId) const;

    void clear(const QString &playerId) const;
};
