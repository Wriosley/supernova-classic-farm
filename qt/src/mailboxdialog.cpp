#include "mailboxdialog.h"
#include "farmapiclient.h"

#include <QDateTime>
#include <QLabel>
#include <QListWidget>
#include <QPushButton>
#include <QVBoxLayout>

namespace {
QString u8(const char *text) { return QString::fromUtf8(text); }
}

MailboxDialog::MailboxDialog(FarmApiClient *client, QWidget *parent)
    : QDialog(parent)
    , client_(client)
{
    setWindowTitle(u8("邮箱"));
    resize(460, 420);
    list_ = new QListWidget;
    auto *closeButton = new QPushButton(u8("关闭"));
    auto *hint = new QLabel(u8("点击未读邮件标记已读。每次打开都会向服务器查询。"));
    hint->setObjectName(QStringLiteral("mutedLabel"));
    hint->setWordWrap(true);
    auto *layout = new QVBoxLayout(this);
    layout->addWidget(hint);
    layout->addWidget(list_);
    layout->addWidget(closeButton);
    connect(closeButton, &QPushButton::clicked, this, &MailboxDialog::accept);
    connect(list_, &QListWidget::itemClicked, this, &MailboxDialog::openSelected);
    connect(client_, &FarmApiClient::mailsUpdated, this, &MailboxDialog::refresh);
    refresh();
}

void MailboxDialog::refresh()
{
    list_->clear();
    const auto mails = client_->mails();
    if (mails.isEmpty()) {
        auto *item = new QListWidgetItem(u8("暂无邮件"));
        item->setFlags(Qt::NoItemFlags);
        list_->addItem(item);
        return;
    }
    for (const auto &mail : mails) {
        const QString stamp = QDateTime::fromMSecsSinceEpoch(mail.createdAtMs).toString(QStringLiteral("yyyy-MM-dd hh:mm"));
        const QString text = QStringLiteral("%1\n%2\n%3\n%4")
            .arg(mail.isRead ? u8("已读") : u8("未读"), mail.title, stamp, mail.content);
        auto *item = new QListWidgetItem(text);
        item->setData(Qt::UserRole, mail.id);
        item->setData(Qt::UserRole + 1, mail.isRead);
        list_->addItem(item);
    }
}

void MailboxDialog::openSelected()
{
    auto *item = list_->currentItem();
    if (!item || item->data(Qt::UserRole).toString().isEmpty())
        return;
    if (item->data(Qt::UserRole + 1).toBool())
        return; // 已读不再请求，避免把 READ_MAIL 当成可重试写操作
    client_->readMail(item->data(Qt::UserRole).toString());
}
