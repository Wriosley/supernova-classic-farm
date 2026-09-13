#include "farmapiclient.h"

#include <QDateTime>
#include <QJsonDocument>
#include <QNetworkReply>
#include <QNetworkRequest>
#include <QTimer>
#include <QUuid>

FarmApiClient::FarmApiClient(QObject *parent)
    : QObject(parent)
{
    heartbeat_.setInterval(20'000);
    connect(&heartbeat_, &QTimer::timeout, this, [this] {
        if (!connected_)
            return;

        Command ping;
        ping.requestId = newRequestId();
        ping.action = QStringLiteral("PING");
        sendCommand(ping, false);
    });

    connect(&socket_, &QWebSocket::connected, this, [this] {
        Command auth;
        auth.requestId = newRequestId();
        auth.action = QStringLiteral("AUTH");
        auth.data.insert(QStringLiteral("token"), token_);
        sendCommand(auth, false);
    });

    connect(&socket_, &QWebSocket::textMessageReceived, this, &FarmApiClient::onMessage);
    connect(&socket_, &QWebSocket::binaryMessageReceived, this, [this](const QByteArray &) {
        socket_.close();
    });

    connect(&socket_, &QWebSocket::disconnected, this, &FarmApiClient::onClose);
}

void FarmApiClient::setServerHostPort(const QString &hostPort)
{
    const QString trimmed = hostPort.trimmed();
    if (trimmed.isEmpty())
        return;

    hostPort_ = trimmed;
    httpBase_ = QStringLiteral("http://") + trimmed;
    wsBase_ = QStringLiteral("ws://") + trimmed;
}

qint64 FarmApiClient::serverNowMs() const
{
    return QDateTime::currentMSecsSinceEpoch() + clockOffsetMs_;
}

QString FarmApiClient::newRequestId() const
{
    return QUuid::createUuid().toString(QUuid::WithoutBraces);
}

void FarmApiClient::setBusy(bool busy)
{
    if (busy_ == busy)
        return;

    busy_ = busy;
    emit busyChanged();
}

void FarmApiClient::setConnected(bool connected)
{
    if (connected_ == connected)
        return;

    connected_ = connected;
    emit connectionChanged();
}

void FarmApiClient::registerAccount(const QString &username, const QString &password)
{
    postLogin(QStringLiteral("/api/register"), 201, username, password, true);
}

void FarmApiClient::login(const QString &username, const QString &password)
{
    postLogin(QStringLiteral("/api/login"), 200, username, password, false);
}

void FarmApiClient::postLogin(const QString &path, int okStatus, const QString &username, const QString &password, bool thenLogin)
{
    if (busy_)
        return;

    setBusy(true);
    QNetworkRequest request(QUrl(httpBase_ + path));
    request.setHeader(QNetworkRequest::ContentTypeHeader, QStringLiteral("application/json"));
    request.setTransferTimeout(15'000);

    const QJsonObject body{
        {QStringLiteral("username"), username},
        {QStringLiteral("password"), password},
    };

    QNetworkReply *reply = http_.post(request, QJsonDocument(body).toJson(QJsonDocument::Compact));
    connect(reply, &QNetworkReply::finished, this, [this, reply, okStatus, thenLogin, username, password] {
        reply->deleteLater();

        const int status = reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt();
        const QJsonObject obj = QJsonDocument::fromJson(reply->readAll()).object();
        const QString code = obj.value(QStringLiteral("code")).toString();
        clockOffsetMs_ = obj.value("server_time_ms").toInteger() - QDateTime::currentMSecsSinceEpoch();

        if (reply->error() != QNetworkReply::NoError && obj.isEmpty()) {
            setBusy(false);
            emit errorMessage(QString::fromUtf8("无法连接游戏服务器"));
            return;
        }

        if (status != okStatus || code != QLatin1String("OK")) {
            setBusy(false);
            emit errorMessage(code + ": " + obj.value(QStringLiteral("message")).toString());
            return;
        }

        if (thenLogin) {
            setBusy(false);
            login(username, password);
            return;
        }

        token_ = obj.value(QStringLiteral("token")).toString();
        playerId_ = obj.value(QStringLiteral("player_id")).toString();
        username_ = username;
        if (token_.isEmpty()) {
            setBusy(false);
            emit errorMessage(QString::fromUtf8("服务器未返回登录凭证"));
            return;
        }

        unconfirmed_ = pendingStore_.load(playerId_);
        emit unconfirmedChanged();
        connectSocket();
    });
}

void FarmApiClient::connectSocket()
{
    entered_ = false;
    heartbeat_.stop();
    setConnected(false);

    if (socket_.state() != QAbstractSocket::UnconnectedState)
        socket_.abort();

    socket_.open(QUrl(wsBase_ + QStringLiteral("/ws")));
}

void FarmApiClient::reconnect()
{
    if (token_.isEmpty()) {
        emit loginRequired(QString::fromUtf8("请重新登录"));
        return;
    }

    setBusy(true);
    connectSocket();
}

void FarmApiClient::logout()
{
    heartbeat_.stop();
    const QString token = token_;
    entered_ = false;
    token_.clear();

    if (!token.isEmpty()) {
        QNetworkRequest request(QUrl(httpBase_ + QStringLiteral("/api/logout")));
        request.setRawHeader("Authorization", QByteArray("Bearer ") + token.toUtf8());
        request.setTransferTimeout(8'000);
        QNetworkReply *reply = http_.post(request, QByteArray());

        connect(reply, &QNetworkReply::finished, reply, &QNetworkReply::deleteLater);
    }

    socket_.abort();
    clearSession();
    emit loggedOut();
}

void FarmApiClient::clearSession()
{
    token_.clear();

    hasSnapshot_ = false;
    snapshot_ = {};
    mails_.clear();
    entered_ = false;
    setConnected(false);
    setBusy(false);

    emit unconfirmedChanged();
    emit mailsUpdated();
}

void FarmApiClient::performWrite(const QString &action, const QJsonObject &data)
{
    if (!connected_) {
        emit errorMessage(QString::fromUtf8("连接已断开，请重新连接"));
        return;
    }
    if (hasUnconfirmed()) {
        emit errorMessage(QString::fromUtf8("上次操作结果尚未确认。请先确认原操作，再进行其他操作。"));
        return;
    }

    Command command;
    command.requestId = newRequestId();
    command.action = action;
    command.data = data;

    pendingStore_.save(playerId_, command);
    sendCommand(command, true);
}

void FarmApiClient::retryUnconfirmed()
{
    if (!hasUnconfirmed())
        return;
    if (!connected_) {
        emit errorMessage(QString::fromUtf8("连接已断开，请重新连接"));
        return;
    }

    // 重试使用原编号，避免重复扣金币。
    sendCommand(unconfirmed_, true);
}

void FarmApiClient::getMailbox()
{
    Command command;
    command.requestId = newRequestId();
    command.action = QStringLiteral("GET_MAILBOX");
    sendCommand(command, false);
}

void FarmApiClient::readMail(const QString &mailId)
{
    Command command;
    command.requestId = newRequestId();
    command.action = QStringLiteral("READ_MAIL");
    command.data.insert(QStringLiteral("mail_id"), mailId);
    sendCommand(command, false);
}

void FarmApiClient::sendCommand(const Command &command, bool write)
{
    if (socket_.state() != QAbstractSocket::ConnectedState) {
        emit errorMessage(QString::fromUtf8("连接已断开，请重新连接"));
        return;
    }

    if (waiting.contains(command.requestId)) {
        emit errorMessage(QString::fromUtf8("请求仍在处理中"));
        return;
    }
    setBusy(true);

    Waiting flight{command, write};
    waiting.insert(command.requestId, flight);

    const QJsonObject body{
        {QStringLiteral("request_id"), command.requestId},
        {QStringLiteral("action"), command.action},
        {QStringLiteral("data"), command.data},
    };
    socket_.sendTextMessage(QString::fromUtf8(QJsonDocument(body).toJson(QJsonDocument::Compact)));

    QTimer::singleShot(10'000, this, [this, id = command.requestId] {
        if (!waiting.contains(id))
            return;
        finishRequest(id, {}, true);
    });
}

void FarmApiClient::finishRequest(const QString &requestId, const QJsonObject &obj, bool timeout)
{
    if (!waiting.contains(requestId))
        return;
    const Waiting flight = waiting.take(requestId);

    setBusy(!waiting.isEmpty());
    const QString code = obj.value(QStringLiteral("code")).toString();

    if (timeout || code == QLatin1String("SERVICE_UNAVAILABLE")) {
        if (flight.write) {
            unconfirmed_ = flight.command;
            pendingStore_.save(playerId_, flight.command);
            emit unconfirmedChanged();
        }

        if (timeout)
            emit errorMessage(QString::fromUtf8("请求超时，结果尚未确认"));
        else
            emit errorMessage(code + ": " + obj.value(QStringLiteral("message")).toString());
        if (flight.command.action == QLatin1String("PING"))
            socket_.close();
        return;
    }

    if (flight.write) {
        unconfirmed_ = {};
        pendingStore_.clear(playerId_);
        emit unconfirmedChanged();
    }

    if (code != QLatin1String("OK"))
        emit errorMessage(code + ": " + obj.value(QStringLiteral("message")).toString());
}

void FarmApiClient::onMessage(const QString &text)
{
    QJsonParseError parseError;
    const QJsonDocument doc = QJsonDocument::fromJson(text.toUtf8(), &parseError);
    if (parseError.error != QJsonParseError::NoError || !doc.isObject()) {
        socket_.close();
        return;
    }

    const QJsonObject obj = doc.object();
    if (obj.value(QStringLiteral("type")).toString() != QLatin1String("response")
        || !obj.contains(QStringLiteral("code"))) {
        socket_.close();
        return;
    }

    clockOffsetMs_ = obj.value("server_time_ms").toInteger() - QDateTime::currentMSecsSinceEpoch();
    const QString code = obj.value(QStringLiteral("code")).toString();
    const QString action = obj.value(QStringLiteral("action")).toString();
    const QString requestId = obj.value(QStringLiteral("request_id")).toString();

    if (code == QLatin1String("UNAUTHENTICATED")) {
        heartbeat_.stop();
        finishRequest(requestId, obj, false);
        clearSession();
        emit loginRequired(QString::fromUtf8("请重新登录"));
        return;
    }

    if (obj.contains("config"))
        config_ = configFromJson(obj.value("config").toObject());
    if (obj.contains("snapshot")) {
        Snapshot next = snapshotFromJson(obj.value("snapshot").toObject());
        // 同版本仍可更新成熟状态，只忽略较旧的快照。
        if (!hasSnapshot_ || next.version >= snapshot_.version) {
            snapshot_ = next;
            hasSnapshot_ = true;
            emit snapshotUpdated();
        }
    }
    if (obj.contains(QStringLiteral("mails"))) {
        mails_ = mailsFromJson(obj.value(QStringLiteral("mails")).toArray());
        emit mailsUpdated();
    }

    if (action == QLatin1String("AUTH") && code == QLatin1String("OK")) {
        Command snap;
        snap.requestId = newRequestId();
        snap.action = QStringLiteral("GET_PLAYER_SNAPSHOT");
        sendCommand(snap, false);
    } else if (action == QLatin1String("GET_PLAYER_SNAPSHOT") && code == QLatin1String("OK") && !entered_) {
        entered_ = true;
        setConnected(true);
        heartbeat_.start();
        emit enteredGame();
        emit statusMessage(QString::fromUtf8("欢迎来到你的农场"));
    }

    finishRequest(requestId, obj, false);
    if (code == QLatin1String("OK") && commandIsWrite(action))
        emit statusMessage(QString::fromUtf8("操作成功，农场已更新"));
}

void FarmApiClient::onClose()
{
    heartbeat_.stop();
    const bool wasEntered = entered_;
    setConnected(false);

    const auto ids = waiting.keys();
    for (const QString &id : ids)
        finishRequest(id, {}, true);

    if (token_.isEmpty())
        return;
    if (!wasEntered) {
        setBusy(false);
        emit errorMessage(QString::fromUtf8("无法连接游戏服务器"));
        return;
    }

    emit errorMessage(QString::fromUtf8("连接已断开，请重新连接；登录过期时请退出后重新登录。"));
}
