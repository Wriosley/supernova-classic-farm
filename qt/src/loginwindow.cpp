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
    // 页面标题和提示文字
    QLabel *title = new QLabel("农场");
    QLabel *subtitle = new QLabel("注册或登录后进入农场");
    hint = new QLabel("登录");

    // 错误提示
    error = new QLabel;
    error->setWordWrap(true);
    error->hide();

    // 服务器、账号和密码输入框
    host = new QLineEdit("127.0.0.1:8080");
    user = new QLineEdit;
    user->setPlaceholderText("student_a");
    user->setMaxLength(32);
    pass = new QLineEdit;
    pass->setEchoMode(QLineEdit::Password);
    pass->setPlaceholderText("6～20 位字母或数字");
    pass->setMaxLength(20);

    // 登录和切换注册按钮
    ok = new QPushButton("登录");
    change = new QPushButton("切换注册");

    // 摆放三个输入框
    QVBoxLayout *form = new QVBoxLayout;
    form->addWidget(new QLabel("服务器"));
    form->addWidget(host);
    form->addWidget(new QLabel("账号（3–32 位小写字母、数字或下划线，以字母开头）"));
    form->addWidget(user);
    form->addWidget(new QLabel("密码"));
    form->addWidget(pass);

    // 摆放整个登录页面
    QVBoxLayout *layout = new QVBoxLayout(this);
    layout->addWidget(title);
    layout->addWidget(subtitle);
    layout->addWidget(hint);
    layout->addWidget(error);
    layout->addLayout(form);
    layout->addWidget(ok);
    layout->addWidget(change);
    layout->addStretch();

    //绑定按钮和事件
    connect(ok, &QPushButton::clicked, this, &LoginWindow::submit);
    connect(change, &QPushButton::clicked, this, [this] {
        isRegister = !isRegister;

        // 切换时只改提示文字，继续保留输入的账号和服务器地址
        hint->setText(isRegister ? "注册" : "登录");
        ok->setText(isRegister ? "注册并进入" : "登录");
        change->setText(isRegister ? "切换登录" : "切换注册");
    });
    // 显示错误
    connect(api, &FarmApiClient::error, this, [this](QString text) {
        error->setText(text);
        error->setVisible(!text.isEmpty());
    });

    // 请求未完成时先禁用按钮，防止连续点击
    connect(api, &FarmApiClient::state, this, [this] {
        bool busy = api->working();
        ok->setEnabled(!busy);
        change->setEnabled(!busy);

        if (busy)
            ok->setText("正在连接…");
        else if (isRegister)
            ok->setText("注册并进入");
        else
            ok->setText("登录");
    });
    
    //进入后清理下密码，常规操作
    connect(api, &FarmApiClient::entered, pass, &QLineEdit::clear);
}

//提交登录请求
void LoginWindow::submit()
{
    error->hide();

    api->setHost(host->text());
    api->enter(user->text().trimmed(), pass->text(), isRegister);
}
