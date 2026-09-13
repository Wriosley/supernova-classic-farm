#include "pendingstore.h"

#include <QJsonDocument>
#include <QSettings>

void PendingStore::save(const QString &playerId, const Command &command) const
{
    if (playerId.isEmpty() || command.requestId.isEmpty() || command.data.contains(QStringLiteral("token")))
        return;

    QSettings settings(QStringLiteral("ClassicFarm"), QStringLiteral("class-mid"));

    const QJsonObject body{
        {QStringLiteral("request_id"), command.requestId},
        {QStringLiteral("action"), command.action},
        {QStringLiteral("data"), command.data},
    };

    settings.setValue(QStringLiteral("pending/") + playerId, QJsonDocument(body).toJson(QJsonDocument::Compact));
}

Command PendingStore::load(const QString &playerId) const
{
    Command command;
    if (playerId.isEmpty())
        return command;
    QSettings settings(QStringLiteral("ClassicFarm"), QStringLiteral("class-mid"));

    const QByteArray raw = settings.value(QStringLiteral("pending/") + playerId).toByteArray();
    if (raw.isEmpty())
        return command;

    const QJsonObject obj = QJsonDocument::fromJson(raw).object();
    command.requestId = obj.value(QStringLiteral("request_id")).toString();
    command.action = obj.value(QStringLiteral("action")).toString();
    command.data = obj.value(QStringLiteral("data")).toObject();

    if (command.requestId.size() < 8 || command.action.isEmpty() || command.data.contains(QStringLiteral("token")))
        return Command{};

    return command;
}

void PendingStore::clear(const QString &playerId) const
{
    if (playerId.isEmpty())
        return;
    QSettings settings(QStringLiteral("ClassicFarm"), QStringLiteral("class-mid"));

    settings.remove(QStringLiteral("pending/") + playerId);
}
