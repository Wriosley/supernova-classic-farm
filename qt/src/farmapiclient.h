#pragma once

#include "models.h"
#include "pendingstore.h"

#include <QHash>
#include <QJsonObject>
#include <QNetworkAccessManager>
#include <QObject>
#include <QTimer>
#include <QWebSocket>

class QNetworkReply;

// 窗口只调用这个类。HTTP 做注册登录，WebSocket 做游戏和邮箱；不连 MySQL。
class FarmApiClient : public QObject
{
    Q_OBJECT
public:
    explicit FarmApiClient(QObject *parent = nullptr);

    void setServerHostPort(const QString &hostPort);
    QString serverHostPort() const { return hostPort_; }
    QString username() const { return username_; }
    QString playerId() const { return playerId_; }
    bool hasToken() const { return !token_.isEmpty(); }
    bool isConnected() const { return connected_; }
    bool isBusy() const { return busy_; }
    bool hasUnconfirmed() const { return !unconfirmed_.requestId.isEmpty(); }
    Command unconfirmed() const { return unconfirmed_; }
    Snapshot snapshot() const { return snapshot_; }
    Config config() const { return config_; }
    QVector<Mail> mails() const { return mails_; }
    qint64 serverNowMs() const; // 用 server_time_ms 修正后的当前时间，倒计时不依赖本机时钟

    void registerAccount(const QString &username, const QString &password);
    void login(const QString &username, const QString &password);
    void logout();
    void reconnect();
    void performWrite(const QString &action, const QJsonObject &data = {});
    void retryUnconfirmed(); // 必须带原来的 request_id，否则服务端会当成新操作
    void getMailbox();
    void readMail(const QString &mailId);

signals:
    void enteredGame();
    void loggedOut();
    void loginRequired(const QString &message);
    void snapshotUpdated();
    void mailsUpdated();
    void statusMessage(const QString &text);
    void errorMessage(const QString &text);
    void connectionChanged();
    void busyChanged();
    void unconfirmedChanged();

private:
    struct InFlight {
        Command command;
        bool write = false;
    };

    QString newRequestId() const;
    void setBusy(bool busy);
    void setConnected(bool connected);
    void applyServerTime(const QJsonObject &obj);
    void postCredentials(const QString &path, int okStatus, const QString &username, const QString &password, bool thenLogin);
    void handleHttpReply(QNetworkReply *reply, int okStatus, bool thenLogin, const QString &username, const QString &password);
    void connectSocket();
    void sendCommand(const Command &command, bool write);
    void handleTextMessage(const QString &text);
    void handleDisconnected();
    void finishInFlight(const QString &requestId, const QJsonObject &obj, bool timeout);
    void applySnapshot(const QJsonObject &obj);
    void clearSession(bool keepPending);

    QString hostPort_ = QStringLiteral("127.0.0.1:8080");
    QString httpBase_ = QStringLiteral("http://127.0.0.1:8080");
    QString wsBase_ = QStringLiteral("ws://127.0.0.1:8080");
    QString username_;
    QString token_; // 只在进程内存，不写文件、不打日志
    QString playerId_;
    Snapshot snapshot_;
    Config config_;
    QVector<Mail> mails_;
    bool hasSnapshot_ = false;
    bool connected_ = false;
    bool busy_ = false;
    bool entered_ = false;
    qint64 clockOffsetMs_ = 0;
    Command unconfirmed_;
    PendingStore pendingStore_;
    QNetworkAccessManager http_;
    QWebSocket socket_;
    QTimer heartbeat_;
    QHash<QString, InFlight> inFlight_; // 用 request_id 匹配尚未返回的 WebSocket 响应
};
