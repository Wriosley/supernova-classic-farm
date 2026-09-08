#pragma once

#include <QWidget>

class FarmApiClient;
class QLineEdit;
class QLabel;
class QPushButton;

// 只负责账号表单；网络全部走 FarmApiClient。
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
    QLineEdit *hostEdit_ = nullptr;
    QLineEdit *userEdit_ = nullptr;
    QLineEdit *passEdit_ = nullptr;
    QLabel *hintLabel_ = nullptr;
    QLabel *errorLabel_ = nullptr;
    QPushButton *submitButton_ = nullptr;
    QPushButton *toggleButton_ = nullptr;
};
