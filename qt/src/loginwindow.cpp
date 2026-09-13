#include "loginwindow.h"
#include "farmapiclient.h"

#include <QLabel>
#include <QLineEdit>
#include <QPushButton>
#include <QVBoxLayout>

LoginWindow::LoginWindow(FarmApiClient *client, QWidget *parent)
    : QWidget(parent)
    , api(client)
{
    setWindowTitle(QString::fromUtf8("农场"));
    auto *title = new QLabel(QString::fromUtf8("农场"));
    auto *subtitle = new QLabel(QString::fromUtf8("注册或登录后进入农场。"));
    hint = new QLabel(QString::fromUtf8("登录"));

    error = new QLabel;
    error->setWordWrap(true);
    error->hide();

    host = new QLineEdit(api->serverHostPort());
    user = new QLineEdit;
    user->setPlaceholderText(QStringLiteral("student_a"));
    user->setMaxLength(32);
    pass = new QLineEdit;
    pass->setEchoMode(QLineEdit::Password);
    pass->setPlaceholderText(QString::fromUtf8("8–128 字节，建议使用字母和数字"));

    pass->setMaxLength(128);

    ok = new QPushButton(QString::fromUtf8("登录"));
    change = new QPushButton(QString::fromUtf8("切换注册"));

    auto *form = new QVBoxLayout;
    form->addWidget(new QLabel(QString::fromUtf8("服务器")));
    form->addWidget(host);
    form->addWidget(new QLabel(QString::fromUtf8("账号")));
    form->addWidget(user);
    auto *userHint = new QLabel(QString::fromUtf8("3–32 位小写字母、数字或下划线，以字母开头。"));
    form->addWidget(userHint);
    form->addWidget(new QLabel(QString::fromUtf8("密码")));
    form->addWidget(pass);

    auto *layout = new QVBoxLayout(this);
    layout->addWidget(title);
    layout->addWidget(subtitle);
    layout->addWidget(hint);
    layout->addWidget(error);
    layout->addLayout(form);
    layout->addWidget(ok);
    layout->addWidget(change);
    layout->addStretch();

    connect(ok, &QPushButton::clicked, this, &LoginWindow::submit);
    connect(pass, &QLineEdit::returnPressed, this, &LoginWindow::submit);
    connect(change, &QPushButton::clicked, this, [this] {
        isRegister = !isRegister;

        hint->setText(isRegister ? QString::fromUtf8("注册") : QString::fromUtf8("登录"));
        ok->setText(isRegister ? QString::fromUtf8("注册并进入") : QString::fromUtf8("登录"));
        change->setText(isRegister ? QString::fromUtf8("切换登录") : QString::fromUtf8("切换注册"));
    });
    connect(api, &FarmApiClient::errorMessage, this, [this](const QString &text) {
        error->setText(text);
        error->setVisible(!text.isEmpty());
    });

    connect(api, &FarmApiClient::busyChanged, this, [this] {
        const bool busy = api->isBusy();
        ok->setEnabled(!busy);
        change->setEnabled(!busy);

        ok->setText(busy ? QString::fromUtf8("正在连接…") : (isRegister ? QString::fromUtf8("注册并进入") : QString::fromUtf8("登录")));
    });
    connect(api, &FarmApiClient::enteredGame, pass, &QLineEdit::clear);
}

void LoginWindow::submit()
{
    error->hide();
    api->setServerHostPort(host->text());

    if (isRegister)
        api->registerAccount(user->text().trimmed(), pass->text());
    else
        api->login(user->text().trimmed(), pass->text());
}
