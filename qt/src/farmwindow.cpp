#include "farmwindow.h"
#include "farmapiclient.h"
#include "mailboxdialog.h"

#include <QFrame>
#include <QGridLayout>
#include <QHBoxLayout>
#include <QJsonObject>
#include <QLabel>
#include <QPushButton>
#include <QSpinBox>
#include <QTimer>
#include <QVBoxLayout>

namespace {
QString u8(const char *text) { return QString::fromUtf8(text); }

QFrame *makeCard()
{
    auto *card = new QFrame;
    card->setObjectName(QStringLiteral("card")); // 对应 main.cpp 里 QFrame#card 样式
    return card;
}
}

FarmWindow::FarmWindow(FarmApiClient *client, QWidget *parent)
    : QWidget(parent)
    , client_(client)
{
    setWindowTitle(u8("我的小农场"));
    accountLabel_ = new QLabel;
    mailboxButton_ = new QPushButton(u8("邮箱"));
    reconnectButton_ = new QPushButton(u8("重新连接"));
    logoutButton_ = new QPushButton(u8("退出登录"));
    logoutButton_->setObjectName(QStringLiteral("secondaryButton"));
    errorLabel_ = new QLabel;
    errorLabel_->setObjectName(QStringLiteral("errorLabel"));
    errorLabel_->setWordWrap(true);
    errorLabel_->hide();
    statusLabel_ = new QLabel;
    statusLabel_->setObjectName(QStringLiteral("successLabel"));
    statusLabel_->setWordWrap(true);
    statusLabel_->hide();
    unconfirmedLabel_ = new QLabel(u8("上次操作结果尚未确认。请先确认原操作，再进行其他操作。"));
    unconfirmedLabel_->setObjectName(QStringLiteral("warningLabel"));
    unconfirmedLabel_->setWordWrap(true);
    retryButton_ = new QPushButton(u8("重试并确认原操作"));
    auto *unconfirmedRow = new QWidget;
    auto *unconfirmedLayout = new QVBoxLayout(unconfirmedRow);
    unconfirmedLayout->setContentsMargins(0, 0, 0, 0);
    unconfirmedLayout->addWidget(unconfirmedLabel_);
    unconfirmedLayout->addWidget(retryButton_);
    unconfirmedRow->setObjectName(QStringLiteral("warningBox"));

    coinsLabel_ = new QLabel;
    seedsLabel_ = new QLabel;
    fertilizerLabel_ = new QLabel;
    cropsLabel_ = new QLabel;
    auto *stats = new QGridLayout;
    const auto addStat = [&](int col, QLabel *value) {
        auto *box = makeCard();
        auto *lay = new QVBoxLayout(box);
        value->setObjectName(QStringLiteral("statValue"));
        lay->addWidget(value);
        stats->addWidget(box, 0, col);
    };
    addStat(0, coinsLabel_);
    addStat(1, seedsLabel_);
    addStat(2, fertilizerLabel_);
    addStat(3, cropsLabel_);

    plotsHost_ = new QWidget;
    plotsLayout_ = new QGridLayout(plotsHost_);
    plotsLayout_->setContentsMargins(0, 0, 0, 0);

    quantitySpin_ = new QSpinBox;
    quantitySpin_->setRange(1, 100);
    quantitySpin_->setValue(1);
    buySeedsButton_ = new QPushButton;
    buyFertilizerButton_ = new QPushButton;
    sellButton_ = new QPushButton;
    shopHint_ = new QLabel;
    shopHint_->setObjectName(QStringLiteral("mutedLabel"));
    shopHint_->setWordWrap(true);
    auto *shop = makeCard();
    auto *shopLayout = new QVBoxLayout(shop);
    shopLayout->addWidget(new QLabel(u8("商店与仓库")));
    shopLayout->addWidget(new QLabel(u8("数量")));
    shopLayout->addWidget(quantitySpin_);
    shopLayout->addWidget(buySeedsButton_);
    shopLayout->addWidget(buyFertilizerButton_);
    shopLayout->addWidget(sellButton_);
    shopLayout->addWidget(shopHint_);

    chapterLabel_ = new QLabel;
    claimButton_ = new QPushButton(u8("领取奖励，进入下一章"));
    auto *tasksCard = makeCard();
    auto *tasksOuter = new QVBoxLayout(tasksCard);
    tasksOuter->addWidget(chapterLabel_);
    tasksLayout_ = new QVBoxLayout;
    tasksOuter->addLayout(tasksLayout_);
    auto *rewardHint = new QLabel(u8("奖励：10 金币 + 3 颗种子"));
    rewardHint->setObjectName(QStringLiteral("mutedLabel"));
    tasksOuter->addWidget(rewardHint);
    tasksOuter->addWidget(claimButton_);

    auto *farmCard = makeCard();
    auto *farmLayout = new QVBoxLayout(farmCard);
    farmLayout->addWidget(new QLabel(u8("我的农田")));
    farmLayout->addWidget(plotsHost_);

    auto *header = new QHBoxLayout;
    auto *titleCol = new QVBoxLayout;
    auto *eyebrow = new QLabel(u8("CLASSIC FARM · 中期课设"));
    eyebrow->setObjectName(QStringLiteral("mutedLabel"));
    auto *title = new QLabel(u8("我的小农场 🌱"));
    title->setObjectName(QStringLiteral("titleLabel"));
    titleCol->addWidget(eyebrow);
    titleCol->addWidget(title);
    header->addLayout(titleCol);
    header->addStretch();
    header->addWidget(accountLabel_);
    header->addWidget(mailboxButton_);
    header->addWidget(reconnectButton_);
    header->addWidget(logoutButton_);

    auto *body = new QHBoxLayout;
    body->addWidget(farmCard, 3);
    auto *side = new QVBoxLayout;
    side->addWidget(shop);
    side->addWidget(tasksCard);
    side->addStretch();
    body->addLayout(side, 2);

    auto *layout = new QVBoxLayout(this);
    layout->setContentsMargins(28, 24, 28, 24);
    layout->addLayout(header);
    layout->addWidget(errorLabel_);
    layout->addWidget(statusLabel_);
    layout->addWidget(unconfirmedRow);
    layout->addLayout(stats);
    layout->addLayout(body);

    auto *tick = new QTimer(this);
    tick->setInterval(1000);
    connect(tick, &QTimer::timeout, this, &FarmWindow::updateClock); // 只刷新倒计时文字，不重建按钮
    tick->start();

    connect(mailboxButton_, &QPushButton::clicked, this, &FarmWindow::openMailbox);
    connect(reconnectButton_, &QPushButton::clicked, client_, &FarmApiClient::reconnect);
    connect(logoutButton_, &QPushButton::clicked, client_, &FarmApiClient::logout);
    connect(retryButton_, &QPushButton::clicked, client_, &FarmApiClient::retryUnconfirmed);
    connect(buySeedsButton_, &QPushButton::clicked, this, [this] {
        client_->performWrite(QStringLiteral("BUY_SEEDS"), QJsonObject{{QStringLiteral("quantity"), quantitySpin_->value()}});
    });
    connect(buyFertilizerButton_, &QPushButton::clicked, this, [this] {
        client_->performWrite(QStringLiteral("BUY_FERTILIZER"), QJsonObject{{QStringLiteral("quantity"), quantitySpin_->value()}});
    });
    connect(sellButton_, &QPushButton::clicked, this, [this] {
        const int crops = client_->snapshot().crops;
        if (crops < 1)
            return;
        client_->performWrite(QStringLiteral("SELL_CROP"), QJsonObject{{QStringLiteral("quantity"), crops}});
    });
    connect(claimButton_, &QPushButton::clicked, this, [this] {
        client_->performWrite(QStringLiteral("CLAIM_CHAPTER_REWARD"));
    });
    connect(client_, &FarmApiClient::snapshotUpdated, this, &FarmWindow::refresh);
    connect(client_, &FarmApiClient::mailsUpdated, this, &FarmWindow::refresh);
    connect(client_, &FarmApiClient::busyChanged, this, &FarmWindow::refresh);
    connect(client_, &FarmApiClient::connectionChanged, this, &FarmWindow::refresh);
    connect(client_, &FarmApiClient::unconfirmedChanged, this, &FarmWindow::refresh);
    connect(client_, &FarmApiClient::errorMessage, this, [this](const QString &text) {
        errorLabel_->setText(text);
        errorLabel_->show();
        statusLabel_->hide();
    });
    connect(client_, &FarmApiClient::statusMessage, this, [this](const QString &text) {
        statusLabel_->setText(text);
        statusLabel_->show();
        errorLabel_->hide();
    });
    refresh();
}

bool FarmWindow::actionsDisabled() const
{
    return client_->isBusy() || !client_->isConnected() || client_->hasUnconfirmed();
}

void FarmWindow::openMailbox()
{
    client_->getMailbox(); // 每次打开都向服务器拉最新已读状态
    MailboxDialog dialog(client_, this);
    dialog.exec();
}

void FarmWindow::refresh()
{
    const Snapshot s = client_->snapshot();
    const Config cfg = client_->config();
    accountLabel_->setText(QStringLiteral("%1 · %2").arg(client_->username(), client_->isConnected() ? u8("在线") : u8("离线")));
    reconnectButton_->setVisible(!client_->isConnected());
    reconnectButton_->setEnabled(!client_->isBusy());
    mailboxButton_->setEnabled(!client_->isBusy() && client_->isConnected());
    const int unread = unreadCount(client_->mails());
    mailboxButton_->setText(unread > 0 ? u8("邮箱（%1）").arg(unread) : u8("邮箱"));
    logoutButton_->setEnabled(!client_->isBusy());
    retryButton_->parentWidget()->setVisible(client_->hasUnconfirmed());
    retryButton_->setEnabled(!client_->isBusy() && client_->isConnected());

    coinsLabel_->setText(u8("金币\n🪙 %1").arg(s.coins));
    seedsLabel_->setText(u8("胡萝卜种子\n🌱 %1").arg(s.seeds));
    fertilizerLabel_->setText(u8("肥料\n🧴 %1").arg(s.fertilizer));
    cropsLabel_->setText(u8("胡萝卜\n🥕 %1").arg(s.crops));

    const int used = s.seeds + s.fertilizer + s.crops;
    // 价格来自服务端 config，界面不写死 2/5。
    buySeedsButton_->setText(u8("买胡萝卜种子 · %1 金币/颗").arg(cfg.seedPrice));
    buyFertilizerButton_->setText(u8("买肥料 · %1 金币/份").arg(cfg.fertilizerPrice));
    sellButton_->setText(u8("出售全部胡萝卜 · %1 金币/个").arg(cfg.cropPrice));
    shopHint_->setText(u8("仓库 %1 / %2。肥料缩短 %3 秒，每株限用一次。")
        .arg(used).arg(cfg.capacity).arg(cfg.fertilizerSeconds));
    const bool disabled = actionsDisabled();
    buySeedsButton_->setEnabled(!disabled);
    buyFertilizerButton_->setEnabled(!disabled);
    sellButton_->setEnabled(!disabled && s.crops > 0);
    claimButton_->setEnabled(!disabled && tasksComplete(s));
    chapterLabel_->setText(u8("第 %1 章 · 农场日常").arg(s.chapter));
    refreshTasks();
    refreshPlots();
}

void FarmWindow::refreshTasks()
{
    while (QLayoutItem *item = tasksLayout_->takeAt(0)) {
        delete item->widget();
        delete item;
    }
    for (const auto &task : client_->snapshot().tasks) {
        auto *row = new QLabel(QStringLiteral("%1 %2    %3/%4")
            .arg(task.current >= task.target ? QStringLiteral("✓") : QStringLiteral("○"),
                 task.label)
            .arg(task.current)
            .arg(task.target));
        tasksLayout_->addWidget(row);
    }
}

void FarmWindow::updateClock()
{
    refreshPlots();
}

void FarmWindow::refreshPlots()
{
    const Snapshot s = client_->snapshot();
    const Config cfg = client_->config();
    const qint64 now = client_->serverNowMs();
    const bool disabled = actionsDisabled();
    // 地块卡片只创建一次，之后只改文字，避免每秒重建导致按钮闪烁。
    while (plotCards_.size() < s.plots.size()) {
        PlotCard plotCard;
        plotCard.card = makeCard();
        auto *lay = new QVBoxLayout(plotCard.card);
        plotCard.number = new QLabel;
        plotCard.number->setObjectName(QStringLiteral("mutedLabel"));
        plotCard.art = new QLabel;
        plotCard.art->setAlignment(Qt::AlignCenter);
        plotCard.art->setObjectName(QStringLiteral("cropArt"));
        plotCard.title = new QLabel;
        plotCard.title->setAlignment(Qt::AlignCenter);
        plotCard.desc = new QLabel;
        plotCard.desc->setAlignment(Qt::AlignCenter);
        plotCard.desc->setObjectName(QStringLiteral("mutedLabel"));
        plotCard.action = new QPushButton;
        lay->addWidget(plotCard.number);
        lay->addWidget(plotCard.art);
        lay->addWidget(plotCard.title);
        lay->addWidget(plotCard.desc);
        lay->addWidget(plotCard.action);
        const int index = plotCards_.size();
        plotsLayout_->addWidget(plotCard.card, index / 2, index % 2);
        plotCards_.append(plotCard);
        connect(plotCard.action, &QPushButton::clicked, this, [this, index] {
            if (index >= client_->snapshot().plots.size())
                return;
            const Plot plot = client_->snapshot().plots.at(index);
            const qint64 nowMs = client_->serverNowMs();
            if (plot.status == QLatin1String("EMPTY"))
                client_->performWrite(QStringLiteral("PLANT"), QJsonObject{{QStringLiteral("plot_id"), plot.id}});
            else if (plot.status == QLatin1String("NEED_CLEANUP"))
                client_->performWrite(QStringLiteral("CLEAN_PLOT"), QJsonObject{{QStringLiteral("plot_id"), plot.id}});
            else if (plotMature(plot, nowMs))
                client_->performWrite(QStringLiteral("HARVEST"), QJsonObject{{QStringLiteral("plot_id"), plot.id}});
            else
                client_->performWrite(QStringLiteral("APPLY_FERTILIZER"), QJsonObject{{QStringLiteral("plot_id"), plot.id}});
        });
    }
    for (int i = 0; i < s.plots.size(); ++i) {
        const Plot plot = s.plots.at(i);
        PlotCard &plotCard = plotCards_[i];
        plotCard.number->setText(u8("%1 号地").arg(plot.id));
        const bool ripe = plotMature(plot, now);
        QString art = u8("🟫");
        QString title = u8("等待播种");
        QString desc = u8("一颗种子，一份期待");
        if (plot.status == QLatin1String("NEED_CLEANUP")) {
            art = u8("🍂");
            title = u8("等待清理");
            desc = u8("清理后可以再次种植");
        } else if (ripe) {
            art = u8("🥕");
            title = u8("胡萝卜成熟了");
            desc = u8("可收获 %1 个胡萝卜").arg(cfg.yield);
        } else if (plot.status == QLatin1String("GROWING")) {
            art = u8("🌱");
            title = u8("胡萝卜生长中");
            desc = u8("预计 %1 秒后成熟").arg(remainingSeconds(plot, now));
        }
        plotCard.art->setText(art);
        plotCard.title->setText(title);
        plotCard.desc->setText(desc);
        if (plot.status == QLatin1String("EMPTY")) {
            plotCard.action->setText(u8("种植胡萝卜"));
            plotCard.action->setEnabled(!disabled && s.seeds > 0);
        } else if (plot.status == QLatin1String("NEED_CLEANUP")) {
            plotCard.action->setText(u8("清理地块"));
            plotCard.action->setEnabled(!disabled);
        } else if (ripe) {
            plotCard.action->setText(u8("收获"));
            plotCard.action->setEnabled(!disabled);
        } else {
            plotCard.action->setText(plot.fertilized ? u8("已施肥") : u8("施肥加速"));
            plotCard.action->setEnabled(!disabled && !plot.fertilized && s.fertilizer > 0);
        }
        plotCard.card->setVisible(true);
    }
}
