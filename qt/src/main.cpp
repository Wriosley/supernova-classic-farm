#include "farmapiclient.h"
#include "farmwindow.h"
#include "loginwindow.h"

#include <QApplication>
#include <QStackedWidget>

int main(int argc, char *argv[])
{
    QApplication app(argc, argv);

    app.setApplicationName(QStringLiteral("Classic Farm"));
    app.setOrganizationName(QStringLiteral("ClassicFarm"));

    FarmApiClient client;

    auto *stack = new QStackedWidget;
    auto *login = new LoginWindow(&client);
    auto *farm = new FarmWindow(&client);
    stack->addWidget(login);
    stack->addWidget(farm);

    stack->setWindowTitle(QString::fromUtf8("农场"));
    stack->resize(800, 650);

    QObject::connect(&client, &FarmApiClient::enteredGame, stack, [stack, farm] {
        stack->setCurrentWidget(farm);
    });
    QObject::connect(&client, &FarmApiClient::loggedOut, stack, [stack, login] {
        stack->setCurrentWidget(login);
    });
    QObject::connect(&client, &FarmApiClient::loginRequired, stack, [stack, login](const QString &) {
        stack->setCurrentWidget(login);
    });

    stack->show();

    return app.exec();
}
