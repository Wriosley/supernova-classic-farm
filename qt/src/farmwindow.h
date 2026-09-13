#pragma once
#include <QWidget>
class FarmApiClient;
class QLabel;
class QListWidget;
class QPushButton;
class QSpinBox;

class FarmWindow : public QWidget
{
    Q_OBJECT
public:
    explicit FarmWindow(FarmApiClient *client, QWidget *parent = nullptr);
private:
    void refresh();
    FarmApiClient *api;
    QLabel *info, *msg, *bag, *prices, *tasks;
    QListWidget *land;
    QSpinBox *num;
    QPushButton *mail, *again, *exitBtn, *retry;
    QPushButton *buy, *buy2, *sell, *plant, *feed, *harvest, *clean, *reward;
};
