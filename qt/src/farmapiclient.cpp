#include "farmapiclient.h"

#include <QDateTime>
#include <QJsonDocument>
#include <QNetworkReply>
#include <QNetworkRequest>
#include <QRegularExpression>
#include <QTimer>
#include <QUuid>

namespace {
QString fromUtf8(const char *text)
{
    return QString::fromUtf8(text);
}

bool validUsername(const QString &username)
{
    static const QRegularExpression re(QStringLiteral("^[a-z][a-z0-9_]{2,31}$"));
    return re.match(username).hasMatch();
}

bool validPassword(const QString &password)
{
    // 与协议一致：按 UTF-8 字节数计，服务端不会裁剪空格。
    const QByteArray bytes = password.toUtf8();
    return bytes.size() >= 8 && bytes.size() <= 128;
}

QString displayError(const QString &code, const QString &message)
{
    // 分支判断用 code；message 只给玩家看。
    if (!message.isEmpty())
        return message;
    if (code.isEmpty())
        return fromUtf8("请求失败，请稍后重试");
    return code;
}
}

FarmApiClient::FarmApiClient(QObject *parent)
    : QObject(parent)
{
    heartbeat_.setInterval(20'000); // 服务端 60 秒无消息会断开，20 秒 PING 保持连接
    connect(&heartbeat_, &QTimer::timeout, this, [this] {
        if (!connected_)
            return;
        Command ping;
        ping.requestId = newRequestId();
        ping.action = QStringLiteral("PING");
        sendCommand(ping, false);
    });
    connect(&socket_, &QWebSocket::connected, this, [this] {
        // 连接后 5 秒内必须 AUTH，身份此后只绑定这条连接。
        Command auth;
        auth.requestId = newRequestId();
        auth.action = QStringLiteral("AUTH");
        auth.data.insert(QStringLiteral("token"), token_);
        sendCommand(auth, false);
    });
    connect(&socket_, &QWebSocket::textMessageReceived, this, &FarmApiClient::handleTextMessage);
    connect(&socket_, &QWebSocket::binaryMessageReceived, this, [this](const QByteArray &) {
        socket_.close(); // 本协议只用文本 JSON，二进制帧视为非法
    });
    connect(&socket_, &QWebSocket::disconnected, this, &FarmApiClient::handleDisconnected);
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
    return QUuid::createUuid().toString(QUuid::WithoutBraces); // 36 字符，符合 8–64 和允许字符集
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

void FarmApiClient::applyServerTime(const QJsonObject &obj)
{
    if (!obj.contains(QStringLiteral("server_time_ms")))
        return;
    clockOffsetMs_ = obj.value(QStringLiteral("server_time_ms")).toInteger() - QDateTime::currentMSecsSinceEpoch();
}

void FarmApiClient::registerAccount(const QString &username, const QString &password)
{
    postCredentials(QStringLiteral("/api/register"), 201, username, password, true);
}

void FarmApiClient::login(const QString &username, const QString &password)
{
    postCredentials(QStringLiteral("/api/login"), 200, username, password, false);
}

void FarmApiClient::postCredentials(const QString &path, int okStatus, const QString &username, const QString &password, bool thenLogin)
{
    if (busy_)
        return;
    if (!validUsername(username) || !validPassword(password)) {
        emit errorMessage(fromUtf8("账号或密码格式不正确"));
        return;
    }
    setBusy(true);
    QNetworkRequest request(QUrl(httpBase_ + path));
    request.setHeader(QNetworkRequest::ContentTypeHeader, QStringLiteral("application/json"));
    request.setTransferTimeout(15'000);
    const QJsonObject body{
        {QStringLiteral("username"), username},
        {QStringLiteral("password"), password}, // 只放 JSON 请求体，不放 URL 或日志
    };
    QNetworkReply *reply = http_.post(request, QJsonDocument(body).toJson(QJsonDocument::Compact));
    connect(reply, &QNetworkReply::finished, this, [this, reply, okStatus, thenLogin, username, password] {
        handleHttpReply(reply, okStatus, thenLogin, username, password);
    });
}

void FarmApiClient::handleHttpReply(QNetworkReply *reply, int okStatus, bool thenLogin, const QString &username, const QString &password)
{
    reply->deleteLater();
    const int status = reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt();
    const QJsonObject obj = QJsonDocument::fromJson(reply->readAll()).object();
    const QString code = obj.value(QStringLiteral("code")).toString();
    applyServerTime(obj);

    if (reply->error() != QNetworkReply::NoError && obj.isEmpty()) {
        setBusy(false);
        emit errorMessage(fromUtf8("无法连接游戏服务器"));
        return;
    }
    if (status != okStatus || code != QLatin1String("OK")) {
        setBusy(false);
        emit errorMessage(displayError(code, obj.value(QStringLiteral("message")).toString()));
        return;
    }
    if (thenLogin) {
        setBusy(false);
        login(username, password); // 注册成功不返回 token，必须再调一次登录
        return;
    }
    token_ = obj.value(QStringLiteral("token")).toString();
    playerId_ = obj.value(QStringLiteral("player_id")).toString();
    username_ = username;
    if (token_.isEmpty()) {
        setBusy(false);
        emit errorMessage(fromUtf8("服务器未返回登录凭证"));
        return;
    }
    unconfirmed_ = pendingStore_.load(playerId_);
    emit unconfirmedChanged();
    connectSocket();
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
        emit loginRequired(fromUtf8("请重新登录"));
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
    token_.clear(); // 先清 token，避免 abort 触发的断线提示误当成掉线
    if (!token.isEmpty()) {
        QNetworkRequest request(QUrl(httpBase_ + QStringLiteral("/api/logout")));
        request.setRawHeader("Authorization", QByteArray("Bearer ") + token.toUtf8());
        request.setTransferTimeout(8'000);
        QNetworkReply *reply = http_.post(request, QByteArray());
        connect(reply, &QNetworkReply::finished, reply, &QNetworkReply::deleteLater);
    }
    socket_.abort();
    clearSession(true);
    emit loggedOut();
}

void FarmApiClient::clearSession(bool keepPending)
{
    token_.clear();
    if (!keepPending)
        unconfirmed_ = {};
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
        emit errorMessage(fromUtf8("连接已断开，请重新连接"));
        return;
    }
    if (hasUnconfirmed()) {
        emit errorMessage(fromUtf8("上次操作结果尚未确认。请先确认原操作，再进行其他操作。"));
        return;
    }
    Command command;
    command.requestId = newRequestId();
    command.action = action;
    command.data = data;
    pendingStore_.save(playerId_, command); // 发出前先记下编号，超时后才能原样重试
    sendCommand(command, true);
}

void FarmApiClient::retryUnconfirmed()
{
    if (!hasUnconfirmed())
        return;
    if (!connected_) {
        emit errorMessage(fromUtf8("连接已断开，请重新连接"));
        return;
    }
    sendCommand(unconfirmed_, true);
}

void FarmApiClient::getMailbox()
{
    Command command;
    command.requestId = newRequestId();
    command.action = QStringLiteral("GET_MAILBOX");
    sendCommand(command, false); // 不带 player_id，服务器用当前认证连接
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
        emit errorMessage(fromUtf8("连接已断开，请重新连接"));
        return;
    }
    if (inFlight_.contains(command.requestId)) {
        emit errorMessage(fromUtf8("请求仍在处理中"));
        return;
    }
    setBusy(true);
    InFlight flight{command, write};
    inFlight_.insert(command.requestId, flight);
    const QJsonObject body{
        {QStringLiteral("request_id"), command.requestId},
        {QStringLiteral("action"), command.action},
        {QStringLiteral("data"), command.data},
    };
    socket_.sendTextMessage(QString::fromUtf8(QJsonDocument(body).toJson(QJsonDocument::Compact)));
    QTimer::singleShot(10'000, this, [this, id = command.requestId] {
        if (!inFlight_.contains(id))
            return;
        finishInFlight(id, {}, true);
    });
}

void FarmApiClient::finishInFlight(const QString &requestId, const QJsonObject &obj, bool timeout)
{
    if (!inFlight_.contains(requestId))
        return;
    const InFlight flight = inFlight_.take(requestId);
    setBusy(!inFlight_.isEmpty());
    const QString code = obj.value(QStringLiteral("code")).toString();
    if (timeout || code == QLatin1String("SERVICE_UNAVAILABLE")) {
        if (flight.write) {
            // 结果未知：保留原编号。明确业务失败才会清掉。
            unconfirmed_ = flight.command;
            pendingStore_.save(playerId_, flight.command);
            emit unconfirmedChanged();
        }
        if (timeout)
            emit errorMessage(fromUtf8("请求超时，结果尚未确认"));
        else
            emit errorMessage(displayError(code, obj.value(QStringLiteral("message")).toString()));
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
        emit errorMessage(displayError(code, obj.value(QStringLiteral("message")).toString()));
}

void FarmApiClient::applySnapshot(const QJsonObject &obj)
{
    const Snapshot next = snapshotFromJson(obj);
    if (hasSnapshot_ && next.version < snapshot_.version)
        return; // 拒绝过期快照；同版本仍要更新，成熟状态可能已变
    snapshot_ = next;
    hasSnapshot_ = true;
    emit snapshotUpdated();
}

void FarmApiClient::handleTextMessage(const QString &text)
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
    applyServerTime(obj);
    const QString code = obj.value(QStringLiteral("code")).toString();
    const QString action = obj.value(QStringLiteral("action")).toString();
    const QString requestId = obj.value(QStringLiteral("request_id")).toString();

    if (code == QLatin1String("UNAUTHENTICATED")) {
        heartbeat_.stop();
        finishInFlight(requestId, obj, false);
        clearSession(true);
        emit loginRequired(fromUtf8("请重新登录"));
        return;
    }

    if (obj.contains(QStringLiteral("snapshot")))
        applySnapshot(obj.value(QStringLiteral("snapshot")).toObject());
    if (obj.contains(QStringLiteral("config")))
        config_ = configFromJson(obj.value(QStringLiteral("config")).toObject());
    if (obj.contains(QStringLiteral("mails"))) {
        mails_ = mailsFromJson(obj.value(QStringLiteral("mails")).toArray());
        emit mailsUpdated(); // 服务器返回完整列表，本地不合并
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
        emit statusMessage(fromUtf8("欢迎来到你的农场"));
    }

    finishInFlight(requestId, obj, false);
    if (code == QLatin1String("OK") && commandIsWrite(action))
        emit statusMessage(fromUtf8("操作成功，农场已更新"));
}

void FarmApiClient::handleDisconnected()
{
    heartbeat_.stop();
    const bool wasEntered = entered_;
    setConnected(false);
    const auto ids = inFlight_.keys();
    for (const QString &id : ids)
        finishInFlight(id, {}, true); // 在途写操作按结果未知处理，保留原 request_id
    if (token_.isEmpty())
        return;
    if (!wasEntered) {
        setBusy(false);
        emit errorMessage(fromUtf8("无法连接游戏服务器"));
        return;
    }
    emit errorMessage(fromUtf8("连接已断开，请重新连接；登录过期时请退出后重新登录。"));
}
