#include "mailboxdialog.h"
#include "farmapiclient.h"

#include <QDateTime>
#include <QLabel>
#include <QListWidget>
#include <QPushButton>
#include <QVBoxLayout>

MailboxDialog::MailboxDialog(FarmApiClient *client, QWidget *parent)
    : QDialog(parent)
    , api(client)
{
    setWindowTitle(QString::fromUtf8("邮箱"));
    resize(460, 420);
    list = new QListWidget;
    auto *closeButton = new QPushButton(QString::fromUtf8("关闭"));
    auto *hint = new QLabel(QString::fromUtf8("点击未读邮件标记已读。每次打开都会向服务器查询。"));
    hint->setWordWrap(true);

    auto *layout = new QVBoxLayout(this);
    layout->addWidget(hint);
    layout->addWidget(list);
    layout->addWidget(closeButton);

    connect(closeButton, &QPushButton::clicked, this, &MailboxDialog::accept);
    connect(list, &QListWidget::itemClicked, this, [this] {
        auto *item = list->currentItem();
        if (!item || item->data(Qt::UserRole).toString().isEmpty())
            return;

        if (item->data(Qt::UserRole + 1).toBool())
            return;

        api->readMail(item->data(Qt::UserRole).toString());
    });
    connect(api, &FarmApiClient::mailsUpdated, this, &MailboxDialog::refresh);

    refresh();
}

void MailboxDialog::refresh()
{
    list->clear();
    const auto mails = api->mails();

    if (mails.isEmpty()) {
        auto *item = new QListWidgetItem(QString::fromUtf8("暂无邮件"));
        item->setFlags(Qt::NoItemFlags);
        list->addItem(item);
        return;
    }

    for (const auto &mail : mails) {
        const QString stamp = QDateTime::fromMSecsSinceEpoch(mail.createdAtMs).toString(QStringLiteral("yyyy-MM-dd hh:mm"));

        const QString text = QStringLiteral("%1\n%2\n%3\n%4")
            .arg(mail.isRead ? QString::fromUtf8("已读") : QString::fromUtf8("未读"), mail.title, stamp, mail.content);
        auto *item = new QListWidgetItem(text);

        item->setData(Qt::UserRole, mail.id);
        item->setData(Qt::UserRole + 1, mail.isRead);

        list->addItem(item);
    }
}
