#include "fakedaemon.h"

#include <QJsonDocument>

FakeDaemon::FakeDaemon(QObject *parent) : QObject(parent)
{
    connect(&m_server, &QLocalServer::newConnection, this, [this] {
        while (QLocalSocket *c = m_server.nextPendingConnection()) {
            m_clients << c;
            connect(c, &QLocalSocket::readyRead, this, [this, c] { onRead(c); });
            connect(c, &QLocalSocket::disconnected, this, [this, c] {
                m_clients.removeAll(c);
                m_buffers.remove(c);
                c->deleteLater();
            });
        }
    });
}

FakeDaemon::~FakeDaemon() { close(); }

bool FakeDaemon::listen(const QString &path)
{
    QLocalServer::removeServer(path);
    m_server.setSocketOptions(QLocalServer::UserAccessOption);
    return m_server.listen(path);
}

void FakeDaemon::dropClients()
{
    const auto cs = m_clients;
    for (QLocalSocket *c : cs)
        c->disconnectFromServer();
}

void FakeDaemon::close()
{
    m_server.close();
    dropClients();
}

void FakeDaemon::push(const QByteArray &rawLine)
{
    for (QLocalSocket *c : std::as_const(m_clients)) {
        c->write(rawLine + '\n');
        c->flush();
    }
}

void FakeDaemon::pushEvent(const QString &kind, const QJsonValue &data)
{
    QJsonObject o{{"event", kind}};
    if (!data.isNull())
        o.insert("data", data);
    push(QJsonDocument(o).toJson(QJsonDocument::Compact));
}

void FakeDaemon::onRead(QLocalSocket *c)
{
    QByteArray &buf = m_buffers[c];
    buf += c->readAll();
    int nl;
    while ((nl = buf.indexOf('\n')) >= 0) {
        const QByteArray line = buf.left(nl);
        buf.remove(0, nl + 1);
        const QJsonObject req = QJsonDocument::fromJson(line).object();
        const QString cmd = req["cmd"].toString();
        received << cmd;
        receivedArgs << req["args"].toObject();
        if (silent)
            continue;
        bool ok = true;
        QString error;
        QJsonValue data;
        if (handler)
            data = handler(cmd, req["args"].toObject(), &ok, &error);
        QJsonObject resp{{"id", req["id"]}, {"ok", ok}};
        if (!ok)
            resp.insert("error", error);
        if (!data.isNull() && !data.isUndefined())
            resp.insert("data", data);
        c->write(QJsonDocument(resp).toJson(QJsonDocument::Compact) + '\n');
        c->flush();
    }
}
