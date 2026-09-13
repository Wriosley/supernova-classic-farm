#include "farmwindow.h"
#include "farmapiclient.h"
#include "mailboxdialog.h"
#include <QHBoxLayout>
#include <QLabel>
#include <QListWidget>
#include <QPushButton>
#include <QSpinBox>
#include <QTimer>
#include <QVBoxLayout>

FarmWindow::FarmWindow(FarmApiClient *client, QWidget *parent)
    : QWidget(parent), api(client)
{
    info = new QLabel;
    msg = new QLabel;
    msg->setWordWrap(true);
    bag = new QLabel;
    prices = new QLabel;
    tasks = new QLabel;
    mail = new QPushButton("邮箱");
    again = new QPushButton("重新连接");
    exitBtn = new QPushButton("退出登录");
    retry = new QPushButton("确认上次操作");
    land = new QListWidget;
    num = new QSpinBox;
    num->setRange(1, 100);
    buy = new QPushButton("买种子");
    buy2 = new QPushButton("买肥料");
    sell = new QPushButton("出售全部胡萝卜");
    plant = new QPushButton("种植");
    feed = new QPushButton("施肥");
    harvest = new QPushButton("收获");
    clean = new QPushButton("清理");
    reward = new QPushButton("领取任务奖励");

    QVBoxLayout *layout = new QVBoxLayout(this);
    QHBoxLayout *top = new QHBoxLayout;
    top->addWidget(info);
    top->addWidget(mail);
    top->addWidget(again);
    top->addWidget(exitBtn);
    layout->addLayout(top);
    layout->addWidget(msg);
    layout->addWidget(retry);
    layout->addWidget(bag);
    layout->addWidget(new QLabel("选择地块，再点击下面的按钮："));
    layout->addWidget(land);
    QHBoxLayout *buttons = new QHBoxLayout;
    buttons->addWidget(plant);
    buttons->addWidget(feed);
    buttons->addWidget(harvest);
    buttons->addWidget(clean);
    layout->addLayout(buttons);
    layout->addWidget(prices);
    QHBoxLayout *shop = new QHBoxLayout;
    shop->addWidget(new QLabel("购买数量"));
    shop->addWidget(num);
    shop->addWidget(buy);
    shop->addWidget(buy2);
    shop->addWidget(sell);
    layout->addLayout(shop);
    layout->addWidget(tasks);
    layout->addWidget(reward);

    connect(mail, &QPushButton::clicked, this, [this] {
        api->getMailbox();
        MailboxDialog dialog(api, this);
        dialog.exec();
    });
    connect(again, &QPushButton::clicked, api, &FarmApiClient::reconnect);
    connect(exitBtn, &QPushButton::clicked, api, &FarmApiClient::logout);
    connect(retry, &QPushButton::clicked, api, &FarmApiClient::retryUnconfirmed);
    connect(buy, &QPushButton::clicked, this, [this] {
        api->performWrite("BUY_SEEDS", QJsonObject{{"quantity", num->value()}});
    });
    connect(buy2, &QPushButton::clicked, this, [this] {
        api->performWrite("BUY_FERTILIZER", QJsonObject{{"quantity", num->value()}});
    });
    connect(sell, &QPushButton::clicked, this, [this] {
        api->performWrite("SELL_CROP", QJsonObject{{"quantity", api->snapshot().crops}});
    });
    // 每个按钮直接取出所选地块的编号，再发送对应操作。
    connect(plant, &QPushButton::clicked, this, [this] {
        if (!land->currentItem()) return;
        int id = land->currentItem()->data(Qt::UserRole).toInt();
        api->performWrite("PLANT", QJsonObject{{"plot_id", id}});
    });
    connect(feed, &QPushButton::clicked, this, [this] {
        if (!land->currentItem()) return;
        int id = land->currentItem()->data(Qt::UserRole).toInt();
        api->performWrite("APPLY_FERTILIZER", QJsonObject{{"plot_id", id}});
    });
    connect(harvest, &QPushButton::clicked, this, [this] {
        if (!land->currentItem()) return;
        int id = land->currentItem()->data(Qt::UserRole).toInt();
        api->performWrite("HARVEST", QJsonObject{{"plot_id", id}});
    });
    connect(clean, &QPushButton::clicked, this, [this] {
        if (!land->currentItem()) return;
        int id = land->currentItem()->data(Qt::UserRole).toInt();
        api->performWrite("CLEAN_PLOT", QJsonObject{{"plot_id", id}});
    });
    connect(reward, &QPushButton::clicked, this, [this] {
        api->performWrite("CLAIM_CHAPTER_REWARD");
    });
    connect(api, &FarmApiClient::snapshotUpdated, this, &FarmWindow::refresh);
    connect(api, &FarmApiClient::connectionChanged, this, &FarmWindow::refresh);
    connect(api, &FarmApiClient::busyChanged, this, &FarmWindow::refresh);
    connect(api, &FarmApiClient::unconfirmedChanged, this, &FarmWindow::refresh);
    connect(api, &FarmApiClient::errorMessage, msg, &QLabel::setText);
    connect(api, &FarmApiClient::statusMessage, msg, &QLabel::setText);
    QTimer *timer = new QTimer(this);
    connect(timer, &QTimer::timeout, this, &FarmWindow::refresh);
    timer->start(1000);
    refresh();
}

void FarmWindow::refresh()
{
    Snapshot s = api->snapshot();
    Config c = api->config();
    info->setText(api->username() + (api->isConnected() ? " 在线" : " 离线"));
    bag->setText(QString("金币：%1   种子：%2   肥料：%3   胡萝卜：%4")
        .arg(s.coins).arg(s.seeds).arg(s.fertilizer).arg(s.crops));
    prices->setText(QString("种子 %1 金币，肥料 %2 金币，胡萝卜售价 %3 金币。仓库：%4/%5")
        .arg(c.seedPrice).arg(c.fertilizerPrice).arg(c.cropPrice)
        .arg(s.seeds + s.fertilizer + s.crops).arg(c.capacity));

    // 每秒只更新文字，保留选中的地块。
    while (land->count() > s.plots.size())
        delete land->takeItem(land->count() - 1);
    for (int i = 0; i < s.plots.size(); i++) {
        Plot p = s.plots[i];
        QString text = "空地";
        if (p.status == "NEED_CLEANUP") {
            text = "需要清理";
        } else if (p.status == "MATURE") {
            text = "胡萝卜已成熟";
        } else if (p.status == "GROWING") {
            qint64 seconds = (p.matureAtMs - api->serverNowMs() + 999) / 1000;
            if (seconds <= 0)
                text = "胡萝卜已成熟";
            else
                text = QString("胡萝卜生长中，剩余 %1 秒").arg(seconds);
            if (p.fertilized) text += "，已施肥";
        }
        if (i >= land->count()) land->addItem("");
        land->item(i)->setText(QString("%1 号地：%2").arg(p.id).arg(text));
        land->item(i)->setData(Qt::UserRole, p.id);
    }
    if (land->currentRow() < 0 && land->count() > 0) land->setCurrentRow(0);

    QString text = QString("第 %1 章\n").arg(s.chapter);
    for (int i = 0; i < s.tasks.size(); i++) {
        Task t = s.tasks[i];
        text += QString("%1：%2/%3\n").arg(t.label).arg(t.current).arg(t.target);
    }
    tasks->setText(text);

    // 库存、地块状态和领奖条件由服务器检查。
    bool ready = api->isConnected() && !api->isBusy();
    bool canWrite = ready && !api->hasUnconfirmed();
    buy->setEnabled(canWrite);
    buy2->setEnabled(canWrite);
    sell->setEnabled(canWrite);
    plant->setEnabled(canWrite && land->count() > 0);
    feed->setEnabled(canWrite && land->count() > 0);
    harvest->setEnabled(canWrite && land->count() > 0);
    clean->setEnabled(canWrite && land->count() > 0);
    reward->setEnabled(canWrite);
    mail->setEnabled(ready);
    again->setEnabled(!api->isConnected() && !api->isBusy());
    exitBtn->setEnabled(!api->isBusy());
    retry->setVisible(api->hasUnconfirmed());
    retry->setEnabled(ready);
}
