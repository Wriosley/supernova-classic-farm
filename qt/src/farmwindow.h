#pragma once

#include <QWidget>

class FarmApiClient;
class QLabel;
class QListWidget;
class QPushButton;
class QSpinBox;
class QComboBox;

class FarmWindow : public QWidget
{
    Q_OBJECT
public:
    FarmWindow(FarmApiClient *client, QWidget *parent = nullptr);

private:
    // 数据变化和倒计时都会调用 refresh，重新显示当前农场。
    void refresh();

    FarmApiClient *api;

    QLabel *info, *msg, *bag, *prices, *tasks;
    QListWidget *land, *stock;
    QComboBox *crop;
    QSpinBox *num;              //购买数量
    QPushButton *mail, *exitBtn, *friendsBtn, *reload;  //上面的按钮
    QPushButton *buy, *buy2, *sell,  *plant, *feed, *harvest, *clean, *reward; //下面的按钮
};
