#pragma once

#include <QJsonArray>
#include <QJsonObject>
#include <QString>
#include <QVector>

// 农田数据
struct Plot {
    int id = 0;
    QString cropName;
    QString status;
    long long matureAtMs = 0;
    bool fertilized = false;
};

// 任务数据
struct Task {
    QString label;
    int current = 0;
    int target = 0;
};

// 邮件数据
struct Mail {
    QString id;
    QString senderName;
    QString title;
    QString content;
    bool isRead = false;
    long long createdAtMs = 0;
};

// 商店作物
struct Crop {
    int id = 0;
    QString name;
    int seedPrice = 0;
    int salePrice = 0;
    int seconds = 0;
    int yield = 0; //产量
};

// 仓库作物数据
struct Bag {
    int id = 0;
    QString name;
    int seeds = 0;
    int crops = 0;
};

// 玩家
struct Friend {
    QString id;
    QString name;
};

// 当前玩家的农场数据
struct Farm {
    int coins = 0;
    int fertilizer = 0;
    int chapter = 1;
    QVector<Plot> plots;
    QVector<Task> tasks;
    QVector<Crop> shop;
    QVector<Bag> inventory;
};

// 按字段读服务器返回的json
inline Farm readFarm(QJsonObject o)
{
    Farm s;
    s.coins = o.value("coins").toInt();
    s.fertilizer = o.value("fertilizer").toInt();
    s.chapter = o.value("chapter").toInt();

    // plots 是地块数组，一块地对应一个 Plot。
    for (QJsonValue v : o.value("plots").toArray()) {
        QJsonObject x = v.toObject();
        Plot p;
        p.id = x.value("plot_id").toInt();
        p.cropName = x.value("crop_name").toString();
        p.status = x.value("status").toString();
        p.matureAtMs = x.value("mature_at_ms").toInteger();
        p.fertilized = x.value("fertilized").toBool();
        s.plots.append(p);
    }

    // 任务、商店和仓库也是同样的读法。
    for (QJsonValue v : o.value("tasks").toArray()) {
        QJsonObject x = v.toObject();
        Task t;
        t.label = x.value("label").toString();
        t.current = x.value("current").toInt();
        t.target = x.value("target").toInt();
        s.tasks.append(t);
    }
    for (QJsonValue v : o.value("shop").toArray()) {
        QJsonObject x = v.toObject();
        Crop c;
        c.id = x.value("crop_id").toInt();
        c.name = x.value("crop_name").toString();
        c.seedPrice = x.value("seed_price").toInt();
        c.salePrice = x.value("sale_price").toInt();
        c.seconds = x.value("mature_seconds").toInt();
        c.yield = x.value("yield").toInt();
        s.shop.append(c);
    }
    for (QJsonValue v : o.value("inventory").toArray()) {
        QJsonObject x = v.toObject();
        Bag item;
        item.id = x.value("crop_id").toInt();
        item.name = x.value("crop_name").toString();
        item.seeds = x.value("seed_count").toInt();
        item.crops = x.value("crop_count").toInt();
        s.inventory.append(item);
    }
    return s;
}
