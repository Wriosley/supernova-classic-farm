#include "farmwindow.h"
#include "farmapiclient.h"
#include "mailboxdialog.h"
#include "frienddialog.h"

#include <QComboBox>
#include <QDateTime>
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
    // 显示数据控件
    info = new QLabel;
    msg = new QLabel;
    bag = new QLabel;
    prices = new QLabel;
    tasks = new QLabel;
    land = new QListWidget;
    stock = new QListWidget; //仓库
    crop = new QComboBox;
    num = new QSpinBox;
    num->setRange(1, 200);

    // 交互按钮
    mail = new QPushButton("邮箱");
    exitBtn = new QPushButton("退出登录");
    friendsBtn = new QPushButton("好友");
    reload = new QPushButton("刷新农场");
    buy = new QPushButton("买种子");
    buy2 = new QPushButton("买肥料");
    sell = new QPushButton("出售所选作物");
    plant = new QPushButton("种植");
    feed = new QPushButton("施肥");
    harvest = new QPushButton("收获");
    clean = new QPushButton("清理");
    reward = new QPushButton("领取任务奖励");

    QVBoxLayout *layout = new QVBoxLayout(this);

    // 顶部：玩家信息、刷新、好友、邮箱、退出
    QHBoxLayout *top = new QHBoxLayout;
    top->addWidget(info);
    top->addWidget(reload);
    top->addWidget(friendsBtn);
    top->addWidget(mail);
    top->addWidget(exitBtn);
    layout->addLayout(top);
    layout->addWidget(msg);
    layout->addWidget(bag);     //金币肥料
    layout->addWidget(new QLabel("选择地块再操作："));

    // 中间：农田、仓库 
    QHBoxLayout *lists = new QHBoxLayout;
    lists->addWidget(land);
    QVBoxLayout *store = new QVBoxLayout;
    store->addWidget(new QLabel("仓库（种子、成品、售价）"));
    store->addWidget(stock);
    lists->addLayout(store);
    layout->addLayout(lists);

    // 农田操作：种植、施肥、收获和清理
    QHBoxLayout *buttons = new QHBoxLayout;
    buttons->addWidget(plant);
    buttons->addWidget(feed);
    buttons->addWidget(harvest);
    buttons->addWidget(clean);
    layout->addLayout(buttons);
    layout->addWidget(new QLabel("购买、种植、出售使用下面选中的作物："));
    layout->addWidget(crop);
    layout->addWidget(prices);

    // 商店操作：数量、购买种子、购买肥料和出售
    QHBoxLayout *shop = new QHBoxLayout;
    shop->addWidget(new QLabel("数量（购买最多100，出售最多200）"));
    shop->addWidget(num);
    shop->addWidget(buy);
    shop->addWidget(buy2);
    shop->addWidget(sell);
    layout->addLayout(shop);
    layout->addWidget(tasks);
    layout->addWidget(reward);

    // 绑定按钮和事件
    // 邮箱和好友打开对话框
    connect(mail, &QPushButton::clicked, this, [this] {
        MailboxDialog dialog(api, this);
        api->game("GET_MAILBOX");
        dialog.exec();
    });
    connect(friendsBtn, &QPushButton::clicked, this, [this] {
        FriendDialog dialog(api, this);
        api->getList();
        dialog.exec();
    });
    // 刷新
    connect(reload, &QPushButton::clicked, this, [this] {
        api->game("GET_PLAYER_SNAPSHOT");
    });
    connect(crop, &QComboBox::currentIndexChanged, this, &FarmWindow::refresh);
    connect(exitBtn, &QPushButton::clicked, api, &FarmApiClient::logout);
    
    connect(buy, &QPushButton::clicked, this, [this] {
        api->game("BUY_SEEDS", {{"crop_id", crop->currentData().toInt()}, {"quantity", num->value()}});
    });
    connect(buy2, &QPushButton::clicked, this, [this] {
        api->game("BUY_FERTILIZER", {{"quantity", num->value()}});
    });
    connect(sell, &QPushButton::clicked, this, [this] {
        api->game("SELL_CROP", {{"crop_id", crop->currentData().toInt()}, {"quantity", num->value()}});
    });

    connect(plant, &QPushButton::clicked, this, [this] {
        if (!land->currentItem()) return;
        //从当前选择的地块中获得地块ID，从crop获得作物ID
        int id = land->currentItem()->data(Qt::UserRole).toInt();
        api->game("PLANT", {{"plot_id", id}, {"crop_id", crop->currentData().toInt()}});
    });
    connect(feed, &QPushButton::clicked, this, [this] {
        if (!land->currentItem()) return;
        int id = land->currentItem()->data(Qt::UserRole).toInt(); 
        api->game("APPLY_FERTILIZER", {{"plot_id", id}});
    });
    connect(harvest, &QPushButton::clicked, this, [this] {
        if (!land->currentItem()) return;
        int id = land->currentItem()->data(Qt::UserRole).toInt(); 
        api->game("HARVEST", {{"plot_id", id}});
    });
    connect(clean, &QPushButton::clicked, this, [this] {
        if (!land->currentItem()) return;
        int id = land->currentItem()->data(Qt::UserRole).toInt();
        api->game("CLEAN_PLOT", {{"plot_id", id}});
    });
    connect(reward, &QPushButton::clicked, this, [this] {
        api->game("CLAIM_CHAPTER_REWARD");
    });

    // 数据或网络状态改变时更新页面
    connect(api, &FarmApiClient::farmChanged, this, &FarmWindow::refresh);
    connect(api, &FarmApiClient::state, this, &FarmWindow::refresh);
    connect(api, &FarmApiClient::error, msg, &QLabel::setText);
    connect(api, &FarmApiClient::farmChanged, msg, &QLabel::clear);
    // 计时器，负责更新剩余秒数,1秒刷新一次
    QTimer *clock = new QTimer(this);
    connect(clock, &QTimer::timeout, this, &FarmWindow::refresh);
    clock->start(1000);
    refresh();
}

void FarmWindow::refresh()
{
    // 拿当前快照
    Farm s = api->farm();
    info->setText(api->user());
    bag->setText(QString("金币：%1   肥料：%2   肥料单价：%3 金币")
        .arg(s.coins).arg(s.fertilizer).arg(api->price()));

    // 重新填下拉框前记住所选作物
    int cropId = crop->currentData().toInt(); //先存下当前index防止后续刷新弹回第一项
    crop->blockSignals(true);
    crop->clear();
    if(crop->count()==0){
        for (Crop item : s.shop)
            crop->addItem(item.name, item.id);
    }
    int index = crop->findData(cropId);
    if (index >= 0) crop->setCurrentIndex(index);
    crop->blockSignals(false);

    // 在商店数组中找到当前作物价格和成熟时间
    prices->clear();
    for (Crop item : s.shop) {
        if (item.id == crop->currentData().toInt())
            prices->setText(QString("种子 %1 金币；%2 秒成熟；每块地产量 %3")
                .arg(item.seedPrice).arg(item.seconds).arg(item.yield));
    }

    //设置一下仓库
    stock->clear();
    for (Bag item : s.inventory) {
        int price = 0;
        for (Crop shopItem : s.shop) {
            if (shopItem.id == item.id) price = shopItem.salePrice;
        }
        stock->addItem(QString("%1：种子 %2，成品 %3，售价 %4 金币")
            .arg(item.name).arg(item.seeds).arg(item.crops).arg(price));
    }

    // 设置地块数据
    int row = land->currentRow();
    land->clear();
    for (Plot p : s.plots) {
        QString text = "空地";
        // 生长中的地块算一下剩余成熟时间
        if (p.status == "GROWING") {
            long long seconds = (p.matureAtMs - QDateTime::currentMSecsSinceEpoch() + 999) / 1000;
            if (seconds > 0)
                text = QString("%1，生长中，剩余 %2 秒").arg(p.cropName).arg(seconds);
            else
                text = p.cropName + "，已成熟";
        }
        if (p.status == "MATURE") text = p.cropName + "，已成熟";
        if (p.status == "NEED_CLEANUP") text = "需要清理";
        if (p.fertilized) text += "，已施肥";

        QListWidgetItem *item = new QListWidgetItem(
            QString("%1 号地：%2").arg(p.id).arg(text));
        item->setData(Qt::UserRole, p.id); //存一下地块ID到田地选项中
        land->addItem(item);
    }

    // 一开始row是-1,这样会让下选框没选任何一项
    land->setCurrentRow(row > 0 ? row : 0);

    // 设置任务列表
    QString taskText = QString("第 %1 章\n").arg(s.chapter);
    for (Task t : s.tasks)
        taskText += QString("%1：%2/%3\n").arg(t.label).arg(t.current).arg(t.target);
    tasks->setText(taskText);

    // 断线或请求未结束阻止操作
    bool canUse = api->online() && !api->working();
    mail->setEnabled(canUse);
    friendsBtn->setEnabled(canUse);
    reload->setEnabled(canUse);
    buy->setEnabled(canUse && crop->count() > 0);
    buy2->setEnabled(canUse);
    sell->setEnabled(canUse && crop->count() > 0);
    plant->setEnabled(canUse && crop->count() > 0 && land->count() > 0);
    feed->setEnabled(canUse);
    harvest->setEnabled(canUse);
    clean->setEnabled(canUse);
    reward->setEnabled(canUse);
    exitBtn->setEnabled(!api->working());
}
