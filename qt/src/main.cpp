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
    // 卡片内按钮不能设成 transparent，否则白字贴在白底上看不见。
    app.setStyleSheet(QString::fromUtf8(R"(
        QWidget { background: #f4f5ed; color: #253b2a; font-size: 14px; }
        QLabel#titleLabel { font-size: 28px; font-weight: 700; }
        QLabel#headingLabel { font-size: 18px; font-weight: 600; }
        QLabel#mutedLabel { color: #687560; font-size: 13px; }
        QLabel#statValue { font-size: 18px; font-weight: 600; }
        QLabel#cropArt { font-size: 42px; }
        QLabel#errorLabel { background: #fbe8e2; color: #873f2b; padding: 10px 12px; border-radius: 8px; }
        QLabel#successLabel { background: #e4efda; color: #345c2f; padding: 10px 12px; border-radius: 8px; }
        QLabel#warningLabel { color: #69531e; }
        QWidget#warningBox { background: #fff0c9; border-radius: 10px; padding: 8px; }
        QFrame#card { background: #fff; border: 1px solid #dfe5d8; border-radius: 14px; }
        QFrame#card QLabel { background: transparent; }
        QLineEdit, QSpinBox { background: #fff; border: 1px solid #ccd6c5; border-radius: 8px; padding: 8px 10px; }
        QPushButton { background: #336b42; color: white; border: 0; border-radius: 8px; padding: 10px 14px; font-weight: 600; }
        QPushButton:disabled { background: #9bb59f; color: #f4f7f0; }
        QPushButton#secondaryButton, QPushButton#textButton { background: #e6edde; color: #304c33; }
        QListWidget { background: #fff; border: 1px solid #dfe5d8; border-radius: 8px; }
    )"));

    FarmApiClient client;
    auto *stack = new QStackedWidget;
    auto *login = new LoginWindow(&client);
    auto *farm = new FarmWindow(&client);
    stack->addWidget(login);
    stack->addWidget(farm);
    stack->setWindowTitle(QString::fromUtf8("我的小农场"));
    stack->resize(1100, 760);

    QObject::connect(&client, &FarmApiClient::enteredGame, stack, [stack, farm] {
        stack->setCurrentWidget(farm); // AUTH 和首份快照成功后才进入农场页
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
