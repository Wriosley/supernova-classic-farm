#pragma once

#include <QJsonArray>
#include <QJsonObject>
#include <QString>
#include <QVector>

// 本文件把 docs/contracts/json-api.md 里的 JSON 转成 Qt 结构。
// 客户端只显示这些数值，不能把金币、成熟时间、产量再提交给服务器。
// 成熟状态按 mature_at_ms 和服务器时间推导，读取快照不会增加 state_version。

struct Plot {
    int id = 0;              // plot_id，1–4
    QString status;          // EMPTY / GROWING / MATURE / NEED_CLEANUP
    qint64 plantedAtMs = 0;  // Unix 毫秒；空地为 0
    qint64 matureAtMs = 0;   // 到点即成熟，无需后台每秒写库
    bool fertilized = false; // 每株肥料只能用一次
};

struct Task {
    QString action; // 与写操作 action 同名，用来累加进度
    QString label;
    int current = 0;
    int target = 0;
};

struct Mail {
    QString id; // mail_id 必须当字符串读，先转 double 会丢掉 BIGINT 精度
    QString title;
    QString content;
    bool isRead = false;
    qint64 createdAtMs = 0;
};

// 玩法参数由服务端权威给出；界面用这些字段显示价格，不要写死后再提交。
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
    quint64 version = 0; // JSON 里是字符串 state_version
    int coins = 0;
    int seeds = 0;
    int fertilizer = 0;
    int crops = 0;
    int chapter = 1;
    QVector<Plot> plots;
    QVector<Task> tasks;
};

// 发给 WebSocket 的一条指令。写操作必须固定 request_id，断线重试不能换新编号。
struct Command {
    QString requestId;
    QString action;
    QJsonObject data;
};

// 只有这些 action 会改农场存档并进入服务端去重表。
// GET_PLAYER_SNAPSHOT、PING、GET_MAILBOX、READ_MAIL 不算写操作。
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

inline Plot plotFromJson(const QJsonObject &o)
{
    Plot p;
    p.id = o.value(QStringLiteral("plot_id")).toInt();
    p.status = o.value(QStringLiteral("status")).toString();
    p.plantedAtMs = o.value(QStringLiteral("planted_at_ms")).toInteger();
    p.matureAtMs = o.value(QStringLiteral("mature_at_ms")).toInteger();
    p.fertilized = o.value(QStringLiteral("fertilized")).toBool();
    return p;
}

inline Task taskFromJson(const QJsonObject &o)
{
    Task t;
    t.action = o.value(QStringLiteral("action")).toString();
    t.label = o.value(QStringLiteral("label")).toString();
    t.current = o.value(QStringLiteral("current")).toInt();
    t.target = o.value(QStringLiteral("target")).toInt();
    return t;
}

inline Mail mailFromJson(const QJsonObject &o)
{
    Mail m;
    m.id = o.value(QStringLiteral("mail_id")).toString();
    m.title = o.value(QStringLiteral("title")).toString();
    m.content = o.value(QStringLiteral("content")).toString();
    m.isRead = o.value(QStringLiteral("is_read")).toBool();
    m.createdAtMs = o.value(QStringLiteral("created_at_ms")).toInteger();
    return m;
}

inline Config configFromJson(const QJsonObject &o)
{
    Config c;
    c.seedPrice = o.value(QStringLiteral("seed_price")).toInt(c.seedPrice);
    c.fertilizerPrice = o.value(QStringLiteral("fertilizer_price")).toInt(c.fertilizerPrice);
    c.cropPrice = o.value(QStringLiteral("crop_price")).toInt(c.cropPrice);
    c.growthSeconds = o.value(QStringLiteral("growth_seconds")).toInt(c.growthSeconds);
    c.fertilizerSeconds = o.value(QStringLiteral("fertilizer_seconds")).toInt(c.fertilizerSeconds);
    c.yield = o.value(QStringLiteral("yield")).toInt(c.yield);
    c.capacity = o.value(QStringLiteral("capacity")).toInt(c.capacity);
    return c;
}

inline Snapshot snapshotFromJson(const QJsonObject &o)
{
    Snapshot s;
    s.playerId = o.value(QStringLiteral("player_id")).toString();
    s.version = o.value(QStringLiteral("state_version")).toString().toULongLong(); // 不要先转 double
    s.coins = o.value(QStringLiteral("coins")).toInt();
    s.seeds = o.value(QStringLiteral("seeds")).toInt();
    s.fertilizer = o.value(QStringLiteral("fertilizer")).toInt();
    s.crops = o.value(QStringLiteral("crops")).toInt();
    s.chapter = o.value(QStringLiteral("chapter")).toInt();
    for (const auto &v : o.value(QStringLiteral("plots")).toArray())
        s.plots.append(plotFromJson(v.toObject()));
    for (const auto &v : o.value(QStringLiteral("tasks")).toArray())
        s.tasks.append(taskFromJson(v.toObject()));
    return s;
}

inline QVector<Mail> mailsFromJson(const QJsonArray &arr)
{
    QVector<Mail> mails;
    mails.reserve(arr.size());
    for (const auto &v : arr)
        mails.append(mailFromJson(v.toObject()));
    return mails;
}

inline int unreadCount(const QVector<Mail> &mails)
{
    int n = 0;
    for (const auto &m : mails) {
        if (!m.isRead)
            ++n;
    }
    return n;
}

inline bool tasksComplete(const Snapshot &s)
{
    if (s.tasks.isEmpty())
        return false;
    for (const auto &t : s.tasks) {
        if (t.current < t.target)
            return false;
    }
    return true;
}

inline qint64 remainingSeconds(const Plot &p, qint64 nowMs)
{
    // 倒计时只用于显示。真正能不能收获由服务端按 mature_at_ms 判断。
    const qint64 delta = p.matureAtMs - nowMs;
    if (delta <= 0)
        return 0;
    return (delta + 999) / 1000;
}

inline bool plotMature(const Plot &p, qint64 nowMs)
{
    return (p.status == QLatin1String("GROWING") || p.status == QLatin1String("MATURE"))
        && remainingSeconds(p, nowMs) == 0;
}
