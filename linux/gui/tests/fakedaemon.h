#pragma once

#include <QJsonObject>
#include <QJsonValue>
#include <QList>
#include <QLocalServer>
#include <QLocalSocket>
#include <QObject>
#include <QStringList>
#include <functional>

// A scriptable stand-in for yggtunneld: listens on a unix socket and speaks the line protocol.
class FakeDaemon : public QObject {
public:
    using Handler = std::function<QJsonValue(const QString &cmd, const QJsonObject &args, bool *ok, QString *error)>;

    explicit FakeDaemon(QObject *parent = nullptr);
    ~FakeDaemon() override;

    bool listen(const QString &path);
    void close();               // stop listening and drop every client
    void dropClients();         // drop clients, keep listening
    void push(const QByteArray &rawLine);                       // any bytes + '\n' to every client
    void pushEvent(const QString &kind, const QJsonValue &data = {});
    int connections() const { return m_clients.size(); }

    Handler handler;            // default: ok, null data
    QStringList received;       // commands in arrival order
    QList<QJsonObject> receivedArgs;
    bool silent = false;        // read requests but never answer

private:
    QLocalServer m_server;
    QList<QLocalSocket *> m_clients;
    QHash<QLocalSocket *, QByteArray> m_buffers;
    void onRead(QLocalSocket *c);
};
