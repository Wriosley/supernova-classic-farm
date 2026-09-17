#include "farmapiclient.h"

#include <QJsonDocument>
#include <QNetworkReply>
#include <QNetworkRequest>
#include <QUuid>

FarmApiClient::FarmApiClient()
{
    heartbeat.setInterval(20000);       //20秒一PING
    //10秒超时，触发一次
    timeout.setInterval(10000);         
    timeout.setSingleShot(true);

    connect(&heartbeat, &QTimer::timeout, this, [this] {
        if (connected && !busy) game("PING");
    });

    // 超时后断开
    connect(&timeout, &QTimer::timeout, &socket, &QWebSocket::abort);
    // WebSocket 连上后用 HTTP 登录得到的 token 做认证
    connect(&socket, &QWebSocket::connected, this, [this] {
        busy = false;
        game("AUTH", {{"token", token}});
    });
    connect(&socket, &QWebSocket::disconnected,this, &FarmApiClient::closed);
    //套接字接收到Message
    connect(&socket, &QWebSocket::textMessageReceived,this, &FarmApiClient::message);
    
}

// 对应登录页面输入地址，格式如下127.0.0.1:8080
void FarmApiClient::setHost(QString text)
{
    httpAddr = "http://" + text;
    wsAddr = "ws://" + text;
}

void FarmApiClient::enter(QString user, QString password, bool reg)
{
    // 防止重复注册/登录
    if (busy)  return;
    busy = true;
    emit state();

    // reg控制登录还是注册
    QString path = reg ? "/api/register" : "/api/login";
    QNetworkRequest req(QUrl(httpAddr + path));
    //添加json请求头
    req.setHeader(QNetworkRequest::ContentTypeHeader, "application/json");
    req.setTransferTimeout(15000);  //最长15秒请求等得
    QJsonObject body{{"username", user}, {"password", password}};
    QNetworkReply *reply = http.post(req, QJsonDocument(body).toJson());
    
    //绑定请求结束操作
    connect(reply, &QNetworkReply::finished, this,
            [this, reply, user, password, reg] {
        //从网络回复中读全部字节转换成JSON
        QJsonObject result = QJsonDocument::fromJson(reply->readAll()).object();
        reply->deleteLater();
        busy = false;
        emit state();

        if (result.value("code").toString() != "OK") {
            QString message = result.value("message").toString();
            if (message.isEmpty())  message = "无法连接服务器";
            emit error(message);
            return;
        }

        // 注册成功后再登录
        if (reg) {
            enter(user, password);
            return;
        }

        // 登录成功后清掉上一个账号的页面数据，再连接游戏 WebSocket。
        name = user;
        token = result.value("token").toString();
        data = {};
        mailList.clear();
        friendList.clear();
        //建立套接字连接，开始计时
        busy = true;
        emit state();
        timeout.start();
        socket.open(QUrl(wsAddr + "/ws"));
    });
}

void FarmApiClient::game(QString action, QJsonObject body)
{
    if (busy)
        return;
    if (socket.state() != QAbstractSocket::ConnectedState) {
        emit error("连接已断开");
        return;
    }

    busy = true;

    // request_id确定响应编号，发送对应Action
    lastId = QUuid::createUuid().toString(QUuid::WithoutBraces);
    QJsonObject request{
        {"request_id", lastId},
        {"action", action},
        {"data", body}
    };
    socket.sendTextMessage(QJsonDocument(request).toJson());
    timeout.start();
    emit state();
}

//获取好友列表
void FarmApiClient::getList()
{
    if (busy || !connected) return;
    busy = true;
    emit state();

    QNetworkRequest req(QUrl(httpAddr + "/api/friends"));
    // http请求头带登录 token，来认证当前玩家
    req.setRawHeader("Authorization", "Bearer " + token.toUtf8());
    req.setTransferTimeout(10000);
    QNetworkReply *reply = http.get(req);
    connect(reply, &QNetworkReply::finished, this, [this, reply] {
        QJsonObject result = QJsonDocument::fromJson(reply->readAll()).object();
        QString code = result.value("code").toString();
        reply->deleteLater();
        busy = false;
        emit state();

        if (code == "UNAUTHENTICATED") { socket.abort(); return; }
        if (code != "OK") {
            emit error(result.value("message").toString());
            return;
        }

        //新数据覆盖
        friendList.clear();
        for (QJsonValue v : result.value("friends").toArray()) {
            QJsonObject x = v.toObject();
            Friend f;
            f.id = x.value("player_id").toString();
            f.name = x.value("username").toString();
            friendList.append(f);
        }
        emit listChanged();
    });
}

// 添加好友
void FarmApiClient::add(QString user)
{
    if (busy || !connected) return;
    busy = true;
    emit state();

    QNetworkRequest req(QUrl(httpAddr + "/api/friends/add"));
    req.setRawHeader("Authorization", "Bearer " + token.toUtf8());
    req.setHeader(QNetworkRequest::ContentTypeHeader, "application/json");
    req.setTransferTimeout(10000);
    QJsonObject body{{"username", user}};
    QNetworkReply *reply = http.post(req, QJsonDocument(body).toJson());
    connect(reply, &QNetworkReply::finished, this, [this, reply] {
        QJsonObject result = QJsonDocument::fromJson(reply->readAll()).object();
        QString code = result.value("code").toString();
        reply->deleteLater();
        busy = false;
        emit state();
        
        if (code == "UNAUTHENTICATED") { socket.abort(); return; }
        if (code != "OK") {
            emit error(result.value("message").toString());
            return;
        }
        
        getList();  // 添加成功以后立刻重新查询
    });
}

// 输入好友玩家ID访问
void FarmApiClient::visit(QString id)
{
    if (busy || !connected) return;
    busy = true;
    emit state();

    QNetworkRequest req(QUrl(httpAddr + "/api/friends/" + id + "/farm"));
    req.setRawHeader("Authorization", "Bearer " + token.toUtf8());
    req.setTransferTimeout(10000);
    QNetworkReply *reply = http.get(req);
    connect(reply, &QNetworkReply::finished, this, [this, reply, id] {
        QJsonObject result = QJsonDocument::fromJson(reply->readAll()).object();
        QString code = result.value("code").toString();
        reply->deleteLater();
        busy = false;
        emit state();

        if (code == "UNAUTHENTICATED") { socket.abort(); return; }
        if (code != "OK") {
            emit error(result.value("message").toString());
            return;
        }

        // 存一下好友地块信息
        QVector<Plot> plots;
        for (QJsonValue v : result.value("plots").toArray()) {
            QJsonObject x = v.toObject();
            Plot p;
            p.id = x.value("plot_id").toInt();
            p.cropName = x.value("crop_name").toString();
            p.status = x.value("status").toString();
            p.matureAtMs = x.value("mature_at_ms").toInteger();
            p.fertilized = x.value("fertilized").toBool();
            plots.append(p);
        }

        //FriendDialog中触发这个信号时显示好友地块
        emit visited(id, plots);
    });
}

void FarmApiClient::steal(QString id, int plotId)
{
    if (busy || !connected) return;
    busy = true;
    emit state();

    //请求偷对应ID玩家plotId的某块田
    QNetworkRequest req(QUrl(httpAddr + "/api/friends/" + id + "/steal"));
    req.setRawHeader("Authorization", "Bearer " + token.toUtf8());
    req.setHeader(QNetworkRequest::ContentTypeHeader, "application/json");
    req.setTransferTimeout(10000);
    QJsonObject body{{"plot_id", plotId}};
    QNetworkReply *reply = http.post(req, QJsonDocument(body).toJson());
    
    connect(reply, &QNetworkReply::finished, this, [this, reply, id] {
        QJsonObject result = QJsonDocument::fromJson(reply->readAll()).object();
        QString code = result.value("code").toString();
        reply->deleteLater();
        busy = false;
        emit state();

        if (code == "UNAUTHENTICATED") { socket.abort(); return; }
        if (code != "OK") {
            emit error(result.value("message").toString());
            return;
        }

        // 偷菜先刷新自己仓库
        data = readFarm(result.value("snapshot").toObject());
        emit farmChanged();
        
        visit(id);  // 偷完再刷新好友农场
    });
}

//发送邮件
void FarmApiClient::send(QString id, QString title, QString content)
{
    if (busy || !connected) return;
    busy = true;
    emit state();

    QNetworkRequest req(QUrl(httpAddr + "/api/friends/" + id + "/mail"));
    req.setRawHeader("Authorization", "Bearer " + token.toUtf8());
    req.setHeader(QNetworkRequest::ContentTypeHeader, "application/json");
    req.setTransferTimeout(10000);
    QJsonObject body{{"title", title}, {"content", content}};
    QNetworkReply *reply = http.post(req, QJsonDocument(body).toJson());
    connect(reply, &QNetworkReply::finished, this, [this, reply] {
        QJsonObject result = QJsonDocument::fromJson(reply->readAll()).object();
        QString code = result.value("code").toString();
        reply->deleteLater();
        busy = false;
        emit state();

        if (code == "UNAUTHENTICATED") { socket.abort(); return; }
        if (code != "OK") {
            emit error(result.value("message").toString());
            return;
        }

        emit sent();
    });
}

// 处理WebSocket从后端接受的信息

void FarmApiClient::message(QString text)
{
    // 读WebSocket 服务器返回的JSON对象
    QJsonObject result = QJsonDocument::fromJson(text.toUtf8()).object();
    QString action = result.value("action").toString();
    QString code = result.value("code").toString();
    QString id = result.value("request_id").toString();

    // 拒绝旧数据
    if (id != lastId) return;
    timeout.stop();
    lastId.clear();
    busy = false;

    if (code == "UNAUTHENTICATED") {
        socket.abort();
        return;
    }
    if (code != "OK") {
        emit error(result.value("message").toString());
        return;
    }
    emit state();

    // 更新对应部分的数据
    if (result.contains("config")) {
        QJsonObject x = result.value("config").toObject();
        fertPrice = x.value("fertilizer_price").toInt();
    }

    if (result.contains("snapshot")) {
        data = readFarm(result.value("snapshot").toObject());
        emit farmChanged();
    }

    //替换一下邮箱信息
    if (action == "GET_MAILBOX" || action == "READ_MAIL") {
        mailList.clear();
        // 邮箱只在这里使用，所以直接把 JSON 转成 Mail 保存
        for (QJsonValue v : result.value("mails").toArray()) {
            QJsonObject x = v.toObject();
            Mail m;
            m.id = x.value("mail_id").toString();
            m.senderName = x.value("sender_username").toString();
            m.title = x.value("title").toString();
            m.content = x.value("content").toString();
            m.isRead = x.value("is_read").toBool();
            m.createdAtMs = x.value("created_at_ms").toInteger();
            mailList.append(m);
        }
        emit mailChanged();
    }

    // 登录完成后要获取农场数据
    if (action == "AUTH") {
        game("GET_PLAYER_SNAPSHOT");
    } 
    else if (action == "GET_PLAYER_SNAPSHOT" && !connected) {
        connected = true;
        heartbeat.start();
        emit state();
        emit entered();
    }

}

void FarmApiClient::closed()
{
    // 恢复计时器状态
    timeout.stop();
    heartbeat.stop();
    busy = false;
    connected = false;
    lastId.clear();

    // token非空说明连接断开，主动退出会清理
    if (!token.isEmpty()) {
        token.clear();
        data = {};
        mailList.clear();
        friendList.clear();
        emit error("连接已断开，请重新登录");
        emit back();
    }
    emit state();
}

//登出
void FarmApiClient::logout()
{
    // 先清掉本地登录状态，再通知服务器删除这次会话
    heartbeat.stop();
    timeout.stop();

    //先请求服务器清理
    if (!token.isEmpty()) {
        QNetworkRequest req(QUrl(httpAddr + "/api/logout"));
        req.setRawHeader("Authorization", "Bearer " + token.toUtf8());
        req.setTransferTimeout(10000);
        QNetworkReply *reply = http.post(req, QByteArray());
        connect(reply, &QNetworkReply::finished,
                reply, &QNetworkReply::deleteLater);
    }

    token.clear();
    lastId.clear();
    connected = false;
    busy = false;

    socket.abort();
    data = {};
    mailList.clear();
    friendList.clear();
    emit state();
    emit back();
}
