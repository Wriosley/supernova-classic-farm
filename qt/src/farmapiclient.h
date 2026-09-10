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

// 桌面客户端的唯一网络入口。窗口不得直连 MySQL，也不得自己算扣款。
//
// 协议分层：
//   HTTP JSON  —— 注册、登录、注销（短请求）
//   WebSocket 文本 JSON —— AUTH、游戏写操作、邮箱、心跳
//
// 登录成功后 token 只留在内存。WebSocket 第一条必须是 AUTH，之后所有业务
// 都绑定这条连接上的玩家，客户端不能提交 player_id。
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

    // 用每次响应里的 server_time_ms 校正本机时钟，倒计时才和服务器成熟时间一致。
    qint64 serverNowMs() const;

    void registerAccount(const QString &username, const QString &password);
    void login(const QString &username, const QString &password);
    void logout();
    void reconnect(); // token 仍有效时只重连 WebSocket，不必再输密码

    // 购买、种植等会改存档的操作。发出前保存 request_id，超时必须原样重试。
    void performWrite(const QString &action, const QJsonObject &data = {});
    void retryUnconfirmed();

    void getMailbox();
    void readMail(const QString &mailId);

signals:
    void enteredGame();                    // AUTH + 首份快照成功，可以切到农场页
    void loggedOut();
    void loginRequired(const QString &message); // token 失效，必须重新登录
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
        bool write = false; // 写操作超时要保留编号；读操作超时不必重放
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
    bool entered_ = false;     // 已经拿到首份快照，断线时才提示“重新连接”
    qint64 clockOffsetMs_ = 0; // server_time_ms - 本机时间
    Command unconfirmed_;
    PendingStore pendingStore_;
    QNetworkAccessManager http_;
    QWebSocket socket_;
    QTimer heartbeat_;
    QHash<QString, InFlight> inFlight_; // 用 request_id 把响应匹配回发出去的那一条
};
