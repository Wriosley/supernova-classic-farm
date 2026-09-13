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

    explicit LoginWindow(FarmApiClient *client, QWidget *parent = nullptr);

private:
    void submit();

    FarmApiClient *api;
    bool isRegister = false;
    QLineEdit *host = nullptr;
    QLineEdit *user = nullptr;
    QLineEdit *pass = nullptr;
    QLabel *hint = nullptr;
    QLabel *error = nullptr;
    QPushButton *ok = nullptr;
    QPushButton *change = nullptr;
};
