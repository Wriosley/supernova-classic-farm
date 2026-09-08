#include "loginwindow.h"
#include "farmapiclient.h"

#include <QLabel>
#include <QLineEdit>
#include <QPushButton>
#include <QVBoxLayout>

namespace {
QString u8(const char *text) { return QString::fromUtf8(text); }
}

// 登录页只收集账号密码；注册成功后由 FarmApiClient 自动再调登录。
LoginWindow::LoginWindow(FarmApiClient *client, QWidget *parent)
    : QWidget(parent)
    , client_(client)
{
    setWindowTitle(u8("我的小农场"));
    auto *title = new QLabel(u8("我的小农场 🌱"));
    title->setObjectName(QStringLiteral("titleLabel"));
    auto *subtitle = new QLabel(u8("种下胡萝卜，照料成长，收获属于你的成果。"));
    subtitle->setObjectName(QStringLiteral("mutedLabel"));
    hintLabel_ = new QLabel(u8("回到你的农场"));
    hintLabel_->setObjectName(QStringLiteral("headingLabel"));
    errorLabel_ = new QLabel;
    errorLabel_->setObjectName(QStringLiteral("errorLabel"));
    errorLabel_->setWordWrap(true);
    errorLabel_->hide();

    hostEdit_ = new QLineEdit(client_->serverHostPort());
    userEdit_ = new QLineEdit;
    userEdit_->setPlaceholderText(QStringLiteral("student_a"));
    userEdit_->setMaxLength(32);
    passEdit_ = new QLineEdit;
    passEdit_->setEchoMode(QLineEdit::Password); // 密码只在提交时进入 HTTP 请求体
    passEdit_->setPlaceholderText(u8("8–128 字节，建议使用字母和数字"));
    passEdit_->setMaxLength(128);

    submitButton_ = new QPushButton(u8("登录农场"));
    toggleButton_ = new QPushButton(u8("没有账号？创建一个"));
    toggleButton_->setObjectName(QStringLiteral("textButton"));
    toggleButton_->setFlat(true);

    auto *form = new QVBoxLayout;
    form->addWidget(new QLabel(u8("服务器")));
    form->addWidget(hostEdit_);
    form->addWidget(new QLabel(u8("账号")));
    form->addWidget(userEdit_);
    auto *userHint = new QLabel(u8("3–32 位小写字母、数字或下划线，以字母开头。"));
    userHint->setObjectName(QStringLiteral("mutedLabel"));
    form->addWidget(userHint);
    form->addWidget(new QLabel(u8("密码")));
    form->addWidget(passEdit_);

    auto *layout = new QVBoxLayout(this);
    layout->setContentsMargins(36, 32, 36, 32);
    layout->addWidget(title);
    layout->addWidget(subtitle);
    layout->addSpacing(12);
    layout->addWidget(hintLabel_);
    layout->addWidget(errorLabel_);
    layout->addLayout(form);
    layout->addSpacing(12);
    layout->addWidget(submitButton_);
    layout->addWidget(toggleButton_, 0, Qt::AlignHCenter);
    layout->addStretch();

    connect(submitButton_, &QPushButton::clicked, this, &LoginWindow::submit);
    connect(passEdit_, &QLineEdit::returnPressed, this, &LoginWindow::submit);
    connect(toggleButton_, &QPushButton::clicked, this, &LoginWindow::toggleMode);
    connect(client_, &FarmApiClient::errorMessage, this, [this](const QString &text) {
        errorLabel_->setText(text);
        errorLabel_->setVisible(!text.isEmpty());
    });
    connect(client_, &FarmApiClient::busyChanged, this, &LoginWindow::refreshBusy);
    connect(client_, &FarmApiClient::enteredGame, passEdit_, &QLineEdit::clear);
}

void LoginWindow::toggleMode()
{
    registerMode_ = !registerMode_;
    hintLabel_->setText(registerMode_ ? u8("开启农场生活") : u8("回到你的农场"));
    submitButton_->setText(registerMode_ ? u8("注册并进入") : u8("登录农场"));
    toggleButton_->setText(registerMode_ ? u8("已有账号？返回登录") : u8("没有账号？创建一个"));
}

void LoginWindow::refreshBusy()
{
    const bool busy = client_->isBusy();
    submitButton_->setEnabled(!busy);
    toggleButton_->setEnabled(!busy);
    submitButton_->setText(busy ? u8("正在连接…") : (registerMode_ ? u8("注册并进入") : u8("登录农场")));
}

void LoginWindow::submit()
{
    errorLabel_->hide();
    client_->setServerHostPort(hostEdit_->text());
    if (registerMode_)
        client_->registerAccount(userEdit_->text().trimmed(), passEdit_->text());
    else
        client_->login(userEdit_->text().trimmed(), passEdit_->text());
}
