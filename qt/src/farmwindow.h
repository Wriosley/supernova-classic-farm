#pragma once

#include <QVector>
#include <QWidget>

class FarmApiClient;
class QGridLayout;
class QLabel;
class QPushButton;
class QSpinBox;
class QVBoxLayout;

struct PlotCard {
    QWidget *card = nullptr;
    QLabel *number = nullptr;
    QLabel *art = nullptr;
    QLabel *title = nullptr;
    QLabel *desc = nullptr;
    QPushButton *action = nullptr;
};

// 农场界面只读 snapshot 刷新；种植等写操作不在本地改金币。
class FarmWindow : public QWidget
{
    Q_OBJECT
public:
    explicit FarmWindow(FarmApiClient *client, QWidget *parent = nullptr);

private:
    void refresh();
    void refreshPlots();
    void refreshTasks();
    void updateClock();
    bool actionsDisabled() const;
    void openMailbox();

    FarmApiClient *client_;
    QLabel *accountLabel_ = nullptr;
    QLabel *errorLabel_ = nullptr;
    QLabel *statusLabel_ = nullptr;
    QLabel *unconfirmedLabel_ = nullptr;
    QPushButton *mailboxButton_ = nullptr;
    QPushButton *reconnectButton_ = nullptr;
    QPushButton *logoutButton_ = nullptr;
    QPushButton *retryButton_ = nullptr;
    QLabel *coinsLabel_ = nullptr;
    QLabel *seedsLabel_ = nullptr;
    QLabel *fertilizerLabel_ = nullptr;
    QLabel *cropsLabel_ = nullptr;
    QWidget *plotsHost_ = nullptr;
    QGridLayout *plotsLayout_ = nullptr;
    QSpinBox *quantitySpin_ = nullptr;
    QPushButton *buySeedsButton_ = nullptr;
    QPushButton *buyFertilizerButton_ = nullptr;
    QPushButton *sellButton_ = nullptr;
    QLabel *shopHint_ = nullptr;
    QLabel *chapterLabel_ = nullptr;
    QVBoxLayout *tasksLayout_ = nullptr;
    QPushButton *claimButton_ = nullptr;
    QVector<PlotCard> plotCards_; // 四块地的固定控件，倒计时只改文字
};
