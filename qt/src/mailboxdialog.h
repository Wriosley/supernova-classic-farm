#pragma once

#include <QDialog>

class FarmApiClient;
class QListWidget;

// 邮箱弹窗。列表完全以 GET_MAILBOX 返回为准，本地不缓存已读。
// READ_MAIL 不是写农场存档的操作，失败也不进入未确认重试队列。
class MailboxDialog : public QDialog
{
    Q_OBJECT
public:
    explicit MailboxDialog(FarmApiClient *client, QWidget *parent = nullptr);

private:
    void refresh();
    void openSelected();

    FarmApiClient *client_;
    QListWidget *list_ = nullptr;
};
