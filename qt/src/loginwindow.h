#pragma once

#include <QWidget>

class FarmApiClient;
class QLineEdit;
class QLabel;
class QPushButton;

// 登录/注册表单。网络、校验结果和进农场都由 FarmApiClient 发信号驱动。
class LoginWindow : public QWidget
{
    Q_OBJECT
public:
    explicit LoginWindow(FarmApiClient *client, QWidget *parent = nullptr);

private:
    void submit();
    void toggleMode();
    void refreshBusy();

    FarmApiClient *client_;
    bool registerMode_ = false;
    QLineEdit *hostEdit_ = nullptr; // 默认 127.0.0.1:8080，局域网可改成服务器 IP
    QLineEdit *userEdit_ = nullptr;
    QLineEdit *passEdit_ = nullptr;
    QLabel *hintLabel_ = nullptr;
    QLabel *errorLabel_ = nullptr;
    QPushButton *submitButton_ = nullptr;
    QPushButton *toggleButton_ = nullptr;
};
