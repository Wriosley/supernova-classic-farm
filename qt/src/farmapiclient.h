#pragma once

#include "models.h"
#include "pendingstore.h"

#include <QHash>
#include <QJsonObject>
#include <QNetworkAccessManager>
#include <QObject>
#include <QTimer>
#include <QWebSocket>

class FarmApiClient : public QObject
{
    Q_OBJECT
public:

    explicit FarmApiClient(QObject *parent = nullptr);

    void setServerHostPort(const QString &hostPort);

    QString serverHostPort() const { return hostPort_; }

    QString username() const { return username_; }

    bool isConnected() const { return connected_; }

    bool isBusy() const { return busy_; }

    bool hasUnconfirmed() const { return !unconfirmed_.requestId.isEmpty(); }

    Snapshot snapshot() const { return snapshot_; }

    Config config() const { return config_; }

    QVector<Mail> mails() const { return mails_; }

    qint64 serverNowMs() const;

    void registerAccount(const QString &username, const QString &password);

    void login(const QString &username, const QString &password);

    void logout();
    void reconnect();

    void performWrite(const QString &action, const QJsonObject &data = {});

    void retryUnconfirmed();

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

    struct Waiting {
        Command command;
        bool write = false;
    };

    QString newRequestId() const;
    void setBusy(bool busy);
    void setConnected(bool connected);

    void postLogin(const QString &path, int okStatus, const QString &username, const QString &password, bool thenLogin);

    void connectSocket();
    void sendCommand(const Command &command, bool write);
    void onMessage(const QString &text);
    void onClose();

    void finishRequest(const QString &requestId, const QJsonObject &obj, bool timeout);
    void clearSession();

    QString hostPort_ = QStringLiteral("127.0.0.1:8080");
    QString httpBase_ = QStringLiteral("http://127.0.0.1:8080");
    QString wsBase_ = QStringLiteral("ws://127.0.0.1:8080");
    QString username_;
    QString token_;
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
    QHash<QString, Waiting> waiting;
};
