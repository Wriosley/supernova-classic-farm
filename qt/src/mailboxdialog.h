#pragma once

#include <QDialog>

class FarmApiClient;
class QListWidget;

class MailboxDialog : public QDialog
{
    Q_OBJECT
public:
    MailboxDialog(FarmApiClient *client, QWidget *parent = nullptr);

private:
    // 使用 api 中最新的邮件重新填充列表。
    void refresh();

    FarmApiClient *api;
    QListWidget *list;
};
