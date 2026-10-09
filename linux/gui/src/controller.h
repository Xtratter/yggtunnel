#pragma once

#include <QJsonObject>
#include <QJsonValue>
#include <QObject>
#include <QStringList>
#include <QTimer>
#include <QUrl>
#include <QVariantList>

class DaemonClient;

// What the QML sees: the daemon's state as properties, the user's actions as methods.
// The private key never passes through here: it is dropped when the status is parsed.
class Controller : public QObject {
    Q_OBJECT
    Q_PROPERTY(bool daemonReachable READ daemonReachable NOTIFY changed)
    Q_PROPERTY(QString reachError READ reachError NOTIFY changed)
    Q_PROPERTY(QString state READ state NOTIFY changed)
    Q_PROPERTY(QString lastError READ lastError NOTIFY changed)
    Q_PROPERTY(bool busy READ busy NOTIFY changed)
    Q_PROPERTY(bool hasProfile READ hasProfile NOTIFY changed)
    Q_PROPERTY(QString profileName READ profileName NOTIFY changed)
    Q_PROPERTY(QString serverAddress READ serverAddress NOTIFY changed)
    Q_PROPERTY(QVariantList peers READ peers NOTIFY changed)
    Q_PROPERTY(QString nodeAddress READ nodeAddress NOTIFY changed)
    Q_PROPERTY(double handshakeAgo READ handshakeAgo NOTIFY changed)
    Q_PROPERTY(bool killSwitch READ killSwitch NOTIFY changed)             // the setting, as the daemon reports it
    Q_PROPERTY(bool allowLan READ allowLan NOTIFY changed)
    Q_PROPERTY(bool killSwitchActive READ killSwitchActive NOTIFY changed) // armed right now; only the daemon says so
    Q_PROPERTY(QString splitMode READ splitMode NOTIFY changed)             // the stored setting: all | exclude | only
    Q_PROPERTY(QStringList splitSubnets READ splitSubnets NOTIFY changed)
    Q_PROPERTY(QStringList splitDomains READ splitDomains NOTIFY changed)
    Q_PROPERTY(QString splitApplied READ splitApplied NOTIFY changed)       // the mode of the running connection, empty when off
    Q_PROPERTY(int splitResolved READ splitResolved NOTIFY changed)         // addresses of the listed names in the firewall
    Q_PROPERTY(QString splitResolveError READ splitResolveError NOTIFY changed)
    Q_PROPERTY(QString nodeLog READ nodeLog NOTIFY logChanged)
    Q_PROPERTY(QStringList history READ history NOTIFY historyChanged)
public:
    explicit Controller(DaemonClient *client, QObject *parent = nullptr);

    bool daemonReachable() const { return m_reachable; }
    QString reachError() const;
    QString state() const { return m_state; }
    QString lastError() const { return m_actionError.isEmpty() ? m_statusError : m_actionError; }
    bool busy() const { return m_busy; }
    bool hasProfile() const { return !m_serverAddress.isEmpty(); }
    QString profileName() const { return m_profileName; }
    QString serverAddress() const { return m_serverAddress; }
    QVariantList peers() const { return m_peers; }
    QString nodeAddress() const { return m_nodeAddress; }
    double handshakeAgo() const { return m_handshakeAgo; }
    bool killSwitch() const { return m_killSwitch; }
    bool allowLan() const { return m_allowLan; }
    bool killSwitchActive() const { return m_ksActive; }
    QString splitMode() const { return m_splitMode; }
    QStringList splitSubnets() const { return m_splitSubnets; }
    QStringList splitDomains() const { return m_splitDomains; }
    QString splitApplied() const { return m_splitApplied; }
    int splitResolved() const { return m_splitResolved; }
    QString splitResolveError() const { return m_splitResolveError; }
    QString nodeLog() const { return m_nodeLog; }
    QStringList history() const { return m_history; }

    void setPollInterval(int ms) { m_poll.setInterval(ms); }

    Q_INVOKABLE void up();
    Q_INVOKABLE void down();
    Q_INVOKABLE void panic();
    Q_INVOKABLE void setKillSwitch(bool on);
    Q_INVOKABLE void setAllowLan(bool allow);
    // Sends the whole routing setting in one `set`; the daemon validates it and refuses a mode change while connected.
    Q_INVOKABLE void setSplit(const QString &mode, const QStringList &subnets, const QStringList &domains);
    Q_INVOKABLE void importLink(const QString &text);
    Q_INVOKABLE void importFile(const QUrl &file); // refuses files over 64 KiB
    Q_INVOKABLE void refreshLog();

signals:
    void changed();
    void logChanged();
    void historyChanged();
    void importFinished(bool ok, const QString &message);
    // a `set` (kill switch, local network, routing) was answered; `error` is the daemon's text when !ok
    void settingsFinished(bool ok, const QString &error);

private:
    void onConnectedChanged(bool connected);
    void onEvent(const QString &kind, const QJsonValue &data);
    void pollStatus();
    void applyStatus(const QJsonObject &s);
    void setState(const QString &state, const QString &error);
    void runAction(const QString &cmd);
    void sendSettings(const QJsonObject &args);

    DaemonClient *m_client;
    QTimer m_poll;
    bool m_reachable = false;
    bool m_busy = false;
    bool m_polling = false;
    bool m_killSwitch = false;
    bool m_allowLan = true;
    bool m_ksActive = false;
    QString m_splitMode = "all";
    QStringList m_splitSubnets, m_splitDomains;
    QString m_splitApplied, m_splitResolveError;
    int m_splitResolved = 0;
    QString m_state = "unreachable";
    QString m_statusError; // the daemon's own last error, replaced by every status
    QString m_actionError; // why this window's last command failed; cleared by the next command or reconnect
    QString m_profileName, m_serverAddress, m_nodeAddress, m_nodeLog;
    QVariantList m_peers;
    double m_handshakeAgo = -1;
    QStringList m_history;
    QString m_lastHistoryKey;
};
