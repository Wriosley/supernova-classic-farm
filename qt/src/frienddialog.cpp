#include "frienddialog.h"
#include "farmapiclient.h"

#include <QHBoxLayout>
#include <QDateTime>
#include <QLabel>
#include <QLineEdit>
#include <QListWidget>
#include <QPlainTextEdit>
#include <QPushButton>
#include <QTimer>
#include <QVBoxLayout>

FriendDialog::FriendDialog(FarmApiClient *client, QWidget *parent)
    : QDialog(parent), api(client)
{
    setWindowTitle("好友");
    resize(850, 650);

    // 添加好友的控件
    user = new QLineEdit;
    user->setPlaceholderText("输入好友账号");
    add = new QPushButton("添加好友");
    reload = new QPushButton("刷新好友");

    // 查看农场的控件
    visit = new QPushButton("查看/刷新好友农场");
    steal = new QPushButton("偷取选中地块");
    list = new QListWidget;
    land = new QListWidget;

    // 发送邮件的控件
    title = new QLineEdit;
    title->setPlaceholderText("邮件标题（最多100字节）");
    content = new QPlainTextEdit;
    content->setPlaceholderText("邮件正文（最多1000字节）");
    sendBtn = new QPushButton("发送邮件");

    // 提示文字和关闭按钮
    info = new QLabel("请选择好友，再查看农场或发送邮件");
    msg = new QLabel;
    msg->setWordWrap(true);
    QPushButton *close = new QPushButton("关闭");

    // 开始界面
    QVBoxLayout *layout = new QVBoxLayout(this);

    // 输入账号、添加好友、刷新好友
    QHBoxLayout *top = new QHBoxLayout;
    top->addWidget(user);
    top->addWidget(add);
    top->addWidget(reload);
    layout->addLayout(top);

    // 错误信息和当前选择好友。
    layout->addWidget(msg);
    layout->addWidget(info);

    // 左好友列表，右好友农田
    QHBoxLayout *lists = new QHBoxLayout;
    lists->addWidget(list);
    lists->addWidget(land);
    layout->addLayout(lists);

    // 农田按钮：查看农场和偷菜
    QHBoxLayout *buttons = new QHBoxLayout;
    buttons->addWidget(visit);
    buttons->addWidget(steal);
    layout->addLayout(buttons);

    // 邮件区域：标题、正文、退出和发送按钮
    layout->addWidget(new QLabel("给选中的好友发邮件："));
    layout->addWidget(title);
    layout->addWidget(content);
    layout->addWidget(sendBtn);
    layout->addWidget(close);

    // 绑定按钮和事件信号
    connect(close, &QPushButton::clicked, this, &QDialog::accept);
    connect(reload, &QPushButton::clicked, api, &FarmApiClient::getList);
    connect(add, &QPushButton::clicked, this, [this] {
        if (user->text().isEmpty()) {
            msg->setText("请输入好友账号");
            return;
        }
        msg->clear();
        api->add(user->text());
    });
    connect(visit, &QPushButton::clicked, this, [this] {
        if (!list->currentItem()) return;
        msg->clear();
        api->visit(list->currentItem()->data(Qt::UserRole).toString());
    });
    connect(steal, &QPushButton::clicked, this, [this] {
        if (!land->currentItem()) return;
        msg->clear();

        // 好友编号在 farmId，地块编号在当前列表项里
        api->steal(farmId, land->currentItem()->data(Qt::UserRole).toInt());
    });
    connect(sendBtn, &QPushButton::clicked, this, [this] {
        if (!list->currentItem()) return;
        QString heading = title->text();
        QString body = content->toPlainText();
        // 后端按 UTF-8 字节数限制长度
        if (heading.isEmpty() || body.isEmpty()) {
            msg->setText("标题和正文不能为空");
            return;
        }
        if (heading.toUtf8().size() > 100 || body.toUtf8().size() > 1000) {
            msg->setText("标题最多100字节，正文最多1000字节");
            return;
        }
        msg->clear();
        api->send(list->currentItem()->data(Qt::UserRole).toString(), heading, body);
    });

    // 换好友后清掉上一位好友的农田
    connect(list, &QListWidget::currentRowChanged, this, [this] {
        farmId.clear();
        land->clear();
        info->setText(list->currentItem()
            ? "当前好友：" + list->currentItem()->text() : "暂无好友，请输入账号添加");
        refresh();
    });

    connect(land, &QListWidget::currentRowChanged, this, &FriendDialog::refresh);
    connect(api, &FarmApiClient::state, this, &FriendDialog::refresh);
    connect(api, &FarmApiClient::error, msg, &QLabel::setText);
    connect(api, &FarmApiClient::back, this, &QDialog::reject);
    // 收到好友列表后刷新
    connect(api, &FarmApiClient::listChanged, this, [this] {
        QString selected; //存一下当前选择的玩家
        if (list->currentItem()) selected = list->currentItem()->data(Qt::UserRole).toString();
        list->clear();
        int row = 0;
        //重新排布一下列表
        for (Friend f : api->list()) {
            QListWidgetItem *item = new QListWidgetItem(f.name + "（ID：" + f.id + "）");
            item->setData(Qt::UserRole, f.id);
            list->addItem(item);
            if (f.id == selected) row = list->count() - 1;
        }

        if (list->count() > 0) 
            list->setCurrentRow(row);
        else 
            info->setText("暂无好友，请输入账号添加");
        refresh();
    });

    //访问玩家农场,plots就是从visited传过来的
    connect(api, &FarmApiClient::visited, this,
            [this](QString id, QVector<Plot> plots) {
        // 防止有延迟,选的B但田是A的
        if (!list->currentItem() || list->currentItem()->data(Qt::UserRole).toString() != id) return;
        int row = land->currentRow();
        farmId = id;
        land->clear();
        
        for (Plot p : plots) {
            QString text = "空地";
            QString status = p.status;
            if (status == "GROWING") {
                long long seconds = (p.matureAtMs - QDateTime::currentMSecsSinceEpoch() + 999) / 1000;
                if (seconds > 0)
                    text = QString("%1，生长中，剩余 %2 秒").arg(p.cropName).arg(seconds);
                else {
                    status = "MATURE";
                    text = p.cropName + "，已成熟";
                }
            }
            if (status == "MATURE") text = p.cropName + "，已成熟";
            if (p.status == "NEED_CLEANUP") text = "需要清理";
            if (p.fertilized) text += "，已施肥";
            QListWidgetItem *item = new QListWidgetItem(QString("%1 号地：%2").arg(p.id).arg(text));
            
            // 保存后面每秒刷新用到的数据
            item->setData(Qt::UserRole, p.id);
            item->setData(Qt::UserRole + 1, status);
            item->setData(Qt::UserRole + 2, p.matureAtMs);
            item->setData(Qt::UserRole + 3, p.cropName);
            item->setData(Qt::UserRole + 4, p.fertilized);
            land->addItem(item);
        }
        land->setCurrentRow(row>0?row:0);
        refresh();
    });

    connect(api, &FarmApiClient::sent, this, [this] {
        msg->setText("邮件发送成功");
        title->clear();
        content->clear();
    });

    // 每秒刷新倒计时，不向服务器请求好友农场。
    QTimer *clock = new QTimer(this);
    connect(clock, &QTimer::timeout, this, &FriendDialog::refresh);
    clock->start(1000);
    refresh();
}


void FriendDialog::refresh()
{
    // 用服务器给的成熟时间减去当前时间，更新好友农田的倒计时。
    for (int i = 0; i < land->count(); ++i) {
        QListWidgetItem *item = land->item(i);
        if (item->data(Qt::UserRole + 1).toString() != "GROWING") continue;
        long long seconds = (item->data(Qt::UserRole + 2).toLongLong()
                          - QDateTime::currentMSecsSinceEpoch() + 999) / 1000;
        QString text;
        if (seconds > 0) {
            text = QString("%1 号地：%2，生长中，剩余 %3 秒")
                .arg(item->data(Qt::UserRole).toInt())
                .arg(item->data(Qt::UserRole + 3).toString())
                .arg(seconds);
        } else {
            item->setData(Qt::UserRole + 1, "MATURE");
            text = QString("%1 号地：%2，已成熟")
                .arg(item->data(Qt::UserRole).toInt())
                .arg(item->data(Qt::UserRole + 3).toString());
        }
        if (item->data(Qt::UserRole + 4).toBool()) text += "，已施肥";
        item->setText(text);
    }

    // 根据连接状态和当前选择，决定按钮能不能点击。
    bool ready = api->online() && !api->working();
    bool selected = list->currentItem() != nullptr;
    add->setEnabled(ready);
    reload->setEnabled(ready);
    visit->setEnabled(ready && selected);
    sendBtn->setEnabled(ready && selected);
    
    bool mature = land->currentItem()
        && land->currentItem()->data(Qt::UserRole + 1).toString() == "MATURE";
    steal->setEnabled(ready && selected && !farmId.isEmpty() && mature);
}
