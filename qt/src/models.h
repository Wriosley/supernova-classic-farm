#pragma once

#include <QJsonArray>
#include <QJsonObject>
#include <QString>
#include <QVector>

struct Plot {
    int id = 0;
    QString status;
    qint64 plantedAtMs = 0;
    qint64 matureAtMs = 0;
    bool fertilized = false;
};

struct Task {
    QString action;
    QString label;
    int current = 0;
    int target = 0;
};

struct Mail {
    QString id;
    QString title;
    QString content;
    bool isRead = false;
    qint64 createdAtMs = 0;
};

struct Config {
    int seedPrice = 2;
    int fertilizerPrice = 2;
    int cropPrice = 5;
    int growthSeconds = 100;
    int fertilizerSeconds = 30;
    int yield = 3;
    int capacity = 200;
};

struct Snapshot {
    QString playerId;
    quint64 version = 0;
    int coins = 0;
    int seeds = 0;
    int fertilizer = 0;
    int crops = 0;
    int chapter = 1;
    QVector<Plot> plots;
    QVector<Task> tasks;
};

struct Command {
    QString requestId;
    QString action;
    QJsonObject data;
};

inline bool commandIsWrite(const QString &action)
{
    return action == QLatin1String("BUY_SEEDS")
        || action == QLatin1String("BUY_FERTILIZER")
        || action == QLatin1String("PLANT")
        || action == QLatin1String("APPLY_FERTILIZER")
        || action == QLatin1String("HARVEST")
        || action == QLatin1String("CLEAN_PLOT")
        || action == QLatin1String("SELL_CROP")
        || action == QLatin1String("CLAIM_CHAPTER_REWARD");
}

inline Config configFromJson(const QJsonObject &o)
{
    Config c;
    c.seedPrice = o.value(QStringLiteral("seed_price")).toInt();
    c.fertilizerPrice = o.value(QStringLiteral("fertilizer_price")).toInt();
    c.cropPrice = o.value(QStringLiteral("crop_price")).toInt();

    c.growthSeconds = o.value(QStringLiteral("growth_seconds")).toInt();
    c.fertilizerSeconds = o.value(QStringLiteral("fertilizer_seconds")).toInt();
    c.yield = o.value(QStringLiteral("yield")).toInt();
    c.capacity = o.value(QStringLiteral("capacity")).toInt();

    return c;
}

inline Snapshot snapshotFromJson(const QJsonObject &o)
{
    Snapshot s;
    s.playerId = o.value(QStringLiteral("player_id")).toString();
    s.version = o.value(QStringLiteral("state_version")).toString().toULongLong();

    s.coins = o.value(QStringLiteral("coins")).toInt();
    s.seeds = o.value(QStringLiteral("seeds")).toInt();
    s.fertilizer = o.value(QStringLiteral("fertilizer")).toInt();
    s.crops = o.value(QStringLiteral("crops")).toInt();
    s.chapter = o.value(QStringLiteral("chapter")).toInt();

    for (const auto &v : o.value(QStringLiteral("plots")).toArray()) {
        QJsonObject item = v.toObject();

        Plot p;
        p.id = item.value(QStringLiteral("plot_id")).toInt();
        p.status = item.value(QStringLiteral("status")).toString();

        p.plantedAtMs = item.value(QStringLiteral("planted_at_ms")).toInteger();
        p.matureAtMs = item.value(QStringLiteral("mature_at_ms")).toInteger();
        p.fertilized = item.value(QStringLiteral("fertilized")).toBool();

        s.plots.append(p);
    }

    for (const auto &v : o.value(QStringLiteral("tasks")).toArray()) {
        QJsonObject item = v.toObject();

        Task t;
        t.action = item.value(QStringLiteral("action")).toString();
        t.label = item.value(QStringLiteral("label")).toString();

        t.current = item.value(QStringLiteral("current")).toInt();
        t.target = item.value(QStringLiteral("target")).toInt();

        s.tasks.append(t);
    }

    return s;
}

inline QVector<Mail> mailsFromJson(const QJsonArray &arr)
{
    QVector<Mail> mails;

    for (const auto &v : arr) {
        QJsonObject item = v.toObject();

        Mail m;
        m.id = item.value(QStringLiteral("mail_id")).toString();

        m.title = item.value(QStringLiteral("title")).toString();
        m.content = item.value(QStringLiteral("content")).toString();
        m.isRead = item.value(QStringLiteral("is_read")).toBool();
        m.createdAtMs = item.value(QStringLiteral("created_at_ms")).toInteger();

        mails.append(m);
    }

    return mails;
}
