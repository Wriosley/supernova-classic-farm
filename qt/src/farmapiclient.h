#pragma once

#include "models.h"

#include <QJsonObject>
#include <QNetworkAccessManager>
#include <QObject>
#include <QTimer>
#include <QWebSocket>

class FarmApiClient : public QObject
{
    Q_OBJECT
public:
    FarmApiClient();

    //  类外部获取当前的数据
    void setHost(QString text);
    QString user() { return name; }
    bool online() { return connected; }
    bool working() { return busy; }
    Farm farm() { return data; }
    int price() { return fertPrice; }
    QVector<Mail> mail() { return mailList; }
    QVector<Friend> list() { return friendList; }

    // 登录、注册和操作
    void enter(QString user, QString password, bool reg = false);   //reg=false/true 登录/注册
    void logout();
    void game(QString action, QJsonObject body = {});

    //好友操作
    void getList();                                                 //请求好友列表
    void add(QString user);                                         
    void visit(QString id);
    void steal(QString id, int plotId);
    void send(QString id, QString title, QString content);

signals:
    // 注册信号
    void entered();
    void back();
    void farmChanged();
    void mailChanged();
    void error(QString text);
    void state();                                       //操作请求信号
                                           
    void listChanged();                                 //好友列表更新
    void visited(QString id, QVector<Plot> plots);
    void sent();

private:
    
    void message(QString text);     // 收到 WebSocket 文字消息
    void closed();                  //连接关闭

    QString httpAddr = "http://127.0.0.1:8080";
    QString wsAddr = "ws://127.0.0.1:8080";
    QString name;       //账号
    QString token;      //登录后后端返回凭证

    QString lastId;     //WebSocket请求编号
    Farm data;          //当前玩家的农场数据
    int fertPrice = 0;
    QVector<Mail> mailList;
    QVector<Friend> friendList;
    bool connected = false;
    bool busy = false;

    QNetworkAccessManager http;
    QWebSocket socket;
    QTimer heartbeat;               //PING
    QTimer timeout;                 //超时
};
