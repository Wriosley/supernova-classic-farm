#pragma once

#include <QDialog>

class FarmApiClient;
class QListWidget;

class MailboxDialog : public QDialog
{
    Q_OBJECT
public:

    explicit MailboxDialog(FarmApiClient *client, QWidget *parent = nullptr);

private:
    void refresh();

    FarmApiClient *api;
    QListWidget *list = nullptr;
};
