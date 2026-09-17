#pragma once

#include <QWidget>

class FarmApiClient;
class QLineEdit;
class QLabel;
class QPushButton;

class LoginWindow : public QWidget
{
    Q_OBJECT
public:
    LoginWindow(FarmApiClient *client, QWidget *parent = nullptr);

private:
    // 登录和注册用同一个提交函数，由 isRegister 决定
    void submit();

    FarmApiClient *api;
    bool isRegister = false;

    QLineEdit *host;
    QLineEdit *user;
    QLineEdit *pass;

    QLabel *hint;           // 登录提示文字
    QLabel *error;

    QPushButton *ok;
    QPushButton *change;
};
