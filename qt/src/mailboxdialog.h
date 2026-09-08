#pragma once

#include <QDialog>

class FarmApiClient;
class QListWidget;

// 邮箱列表以服务器返回为准；点击未读才发 READ_MAIL。
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
