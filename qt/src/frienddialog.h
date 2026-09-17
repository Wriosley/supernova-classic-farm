#pragma once

#include <QDialog>

class FarmApiClient;
class QListWidget;
class QLineEdit;
class QPlainTextEdit;
class QPushButton;
class QLabel;

class FriendDialog : public QDialog
{
    Q_OBJECT
public:
    FriendDialog(FarmApiClient *client, QWidget *parent = nullptr);

private:
    // 根据连接状态和当前选择，决定哪些按钮可以点击。
    void refresh();

    FarmApiClient *api;
    // 正在查看的好友编号，偷菜时需要一起发给服务器。
    QString farmId;
    QListWidget *list, *land;
    QLineEdit *user, *title;
    QPlainTextEdit *content;
    QLabel *info, *msg;
    QPushButton *add, *reload, *visit, *steal, *sendBtn;
};
