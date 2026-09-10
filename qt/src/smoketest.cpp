#include "farmapiclient.h"

#include <QCoreApplication>
#include <QEventLoop>
#include <QTimer>
#include <QUuid>
#include <cstdio>

// 无窗口冒烟测试，用来确认 Qt 能连上正在运行的 Go 服务器。
// 流程：注册并自动登录 → 核对初始快照 → BUY_SEEDS 3 → GET_MAILBOX。
// 不要打印 token 或密码。需要先启动 server/cmd/game（可用 -Memory）。
static QString uniqueUser()
{
    return QStringLiteral("qt") + QUuid::createUuid().toString(QUuid::Id128).left(10).toLower();
}

int main(int argc, char *argv[])
{
    QCoreApplication app(argc, argv);
    app.setOrganizationName(QStringLiteral("ClassicFarm"));
    app.setApplicationName(QStringLiteral("class-mid-smoke"));

    FarmApiClient client;
    const QString username = uniqueUser();
    const QString password = QStringLiteral("password1");

    QEventLoop loop;
    int code = 2;
    QObject::connect(&client, &FarmApiClient::enteredGame, &loop, [&] {
        const Snapshot s = client.snapshot();
        // 与服务端 NewState 对齐：10 金币、0 种子、1 肥料、4 块地。
        if (s.coins != 10 || s.seeds != 0 || s.fertilizer != 1 || s.plots.size() != 4) {
            std::fprintf(stderr, "unexpected initial snapshot\n");
            code = 3;
            loop.quit();
            return;
        }
        QObject::connect(&client, &FarmApiClient::snapshotUpdated, &loop, [&] {
            const Snapshot after = client.snapshot();
            if (after.coins != 4 || after.seeds != 3)
                return; // 忽略 PING/倒计时触发的同余额刷新，只认买种子成功后的快照
            QObject::connect(&client, &FarmApiClient::mailsUpdated, &loop, [&] {
                if (client.mails().isEmpty() || client.mails().first().title.isEmpty()) {
                    std::fprintf(stderr, "welcome mail missing\n");
                    code = 5;
                    loop.quit();
                    return;
                }
                std::fprintf(stdout, "qt smoke ok\n");
                code = 0;
                loop.quit();
            }, Qt::SingleShotConnection);
            client.getMailbox();
        });
        client.performWrite(QStringLiteral("BUY_SEEDS"), QJsonObject{{QStringLiteral("quantity"), 3}});
    });
    QObject::connect(&client, &FarmApiClient::errorMessage, &loop, [&](const QString &text) {
        std::fprintf(stderr, "%s\n", qUtf8Printable(text));
        code = 1;
        loop.quit();
    });
    QTimer::singleShot(20'000, &loop, [&] {
        std::fprintf(stderr, "smoke timeout\n");
        code = 6;
        loop.quit();
    });
    client.registerAccount(username, password);
    loop.exec();
    return code;
}
