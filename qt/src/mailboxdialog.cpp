#include "mailboxdialog.h"
#include "farmapiclient.h"

#include <QDateTime>
#include <QLabel>
#include <QListWidget>
#include <QPushButton>
#include <QVBoxLayout>

MailboxDialog::MailboxDialog(FarmApiClient *client, QWidget *parent)
    : QDialog(parent), api(client)
{
    setWindowTitle("邮箱");
    resize(460, 420);

    // 邮件列表、提示文字和关闭按钮
    list = new QListWidget;
    QPushButton *closeButton = new QPushButton("关闭");
    QLabel *hint = new QLabel("点击未读邮件标记已读");
    hint->setWordWrap(true);

    // 摆放邮箱界面
    QVBoxLayout *layout = new QVBoxLayout(this);
    layout->addWidget(hint);
    layout->addWidget(list);
    layout->addWidget(closeButton);

    // 绑定按钮和事件
    connect(closeButton, &QPushButton::clicked, this, &MailboxDialog::accept);
    connect(list, &QListWidget::itemClicked, this, [this] {
        QListWidgetItem *item = list->currentItem();

        // 空提示项/已经读过的邮件不用再次发送请求
        if (!item || item->data(Qt::UserRole).toString().isEmpty())
            return;

        if (item->data(Qt::UserRole + 1).toBool())
            return;

        api->game("READ_MAIL", {{"mail_id", item->data(Qt::UserRole).toString()}});
    });
    // 邮件变化刷新列表，请求期间禁止点击
    connect(api, &FarmApiClient::mailChanged, this, &MailboxDialog::refresh);
    connect(api, &FarmApiClient::state, this, [this] {
        list->setEnabled(api->online() && !api->working());
    });
    connect(api, &FarmApiClient::error, hint, &QLabel::setText);
    connect(api, &FarmApiClient::back, this, &QDialog::reject);

    refresh();
}

void MailboxDialog::refresh()
{
    list->clear();
    QVector<Mail> mails = api->mail();

    // 空邮箱也放一行提示
    if (mails.isEmpty()) {
        QListWidgetItem *item = new QListWidgetItem("暂无邮件");
        item->setFlags(Qt::NoItemFlags);
        list->addItem(item);
        return;
    }

    for (Mail mail : mails) {
        //
        QString stamp = QDateTime::fromMSecsSinceEpoch(mail.createdAtMs).toString("yyyy-MM-dd hh:mm");

        // 系统邮件无用户名,玩家邮件显示真实账号名
        QString sender = mail.senderName.isEmpty() ? "系统" : mail.senderName;
        QString text = QString("%1　发送者：%2\n%3\n%4\n%5")
            .arg(mail.isRead ? "已读" : "未读", sender, mail.title, stamp, mail.content);
        QListWidgetItem *item = new QListWidgetItem(text);

        // 邮件编号和已读状态不直接显示，点击时从 UserRole 取出来。
        item->setData(Qt::UserRole, mail.id);
        item->setData(Qt::UserRole + 1, mail.isRead);

        list->addItem(item);
    }
}
