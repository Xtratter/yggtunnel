#pragma once

#include <QByteArray>
#include <QHash>
#include <QJsonObject>
#include <QJsonValue>
#include <QLocalSocket>
#include <QObject>
#include <QTimer>
#include <functional>

// One connection to yggtunneld: calls with replies, pushed events, and reconnects while the daemon is away.
class DaemonClient : public QObject {
    Q_OBJECT
public:
    using Done = std::function<void(bool ok, const QString &error, const QJsonValue &data)>;

    explicit DaemonClient(QString socketPath, QObject *parent = nullptr);

    bool isConnected() const;
    QString lastConnectError() const { return m_lastError; } // human text; empty while connected
    QString socketPath() const { return m_path; }
    void setRetryInterval(int ms) { m_retry.setInterval(ms); }

    // Connects now, then retries while disconnected.
    void start();
    // `done` runs exactly once: with the reply, or with ok=false when the daemon is unreachable
    // or the connection is lost before the reply arrives.
    void call(const QString &cmd, const QJsonObject &args, Done done);

signals:
    void connectedChanged(bool connected);
    void lastConnectErrorChanged();
    void eventReceived(const QString &kind, const QJsonValue &data);

private:
    void tryConnect();
    void onReadyRead();
    void onDisconnected();
    void failPending(const QString &why);
    static QString describe(QLocalSocket::LocalSocketError e, const QString &path, const QString &raw);

    QString m_path;
    QLocalSocket *m_sock = nullptr;
    QTimer m_retry;
    QByteArray m_buf;
    bool m_skipLine = false; // an oversized line is being discarded up to its newline
    int m_nextId = 0;
    QHash<int, Done> m_pending;
    QString m_lastError;
    bool m_wasConnected = false;
};
