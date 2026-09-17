#include "farmapiclient.h"
#include "farmwindow.h"
#include "loginwindow.h"

#include <QApplication>
#include <QStackedWidget>

int main(int argc, char *argv[])
{
    QApplication app(argc, argv);

    // 连接对象，登录页和农场页共用
    FarmApiClient client;

    QStackedWidget *stack = new QStackedWidget;
    LoginWindow *login = new LoginWindow(&client);
    FarmWindow *farm = new FarmWindow(&client);
    stack->addWidget(login);
    stack->addWidget(farm);

    stack->setWindowTitle("课设QQ农场");
    stack->resize(1000, 760);

    // 登录成功进入农场，退出或断线回到登录页
    QObject::connect(&client, &FarmApiClient::entered, stack, [stack, farm] {
        stack->setCurrentWidget(farm);
    });
    QObject::connect(&client, &FarmApiClient::back, stack, [stack, login] {
        stack->setCurrentWidget(login);
    });

    stack->show();

    return app.exec();
}
