#include "daemonclient.h"

#include "protocol.h"

DaemonClient::DaemonClient(QString socketPath, QObject *parent) : QObject(parent), m_path(std::move(socketPath))
{
    m_retry.setInterval(2000);
    connect(&m_retry, &QTimer::timeout, this, [this] {
        if (!m_sock || m_sock->state() == QLocalSocket::UnconnectedState)
            tryConnect();
    });
}

bool DaemonClient::isConnected() const
{
    return m_sock && m_sock->state() == QLocalSocket::ConnectedState;
}

void DaemonClient::start()
{
    m_retry.start();
    tryConnect();
}

QString DaemonClient::describe(QLocalSocket::LocalSocketError e, const QString &path, const QString &raw)
{
    switch (e) {
    case QLocalSocket::ServerNotFoundError:
        return tr("socket not found: %1").arg(path);
    case QLocalSocket::ConnectionRefusedError:
        return tr("connection refused: the daemon is not accepting connections on %1").arg(path);
    case QLocalSocket::SocketAccessError:
        return tr("permission denied on %1 (is your user in the group yggtunnel?)").arg(path);
    default:
        return raw;
    }
}

void DaemonClient::tryConnect()
{
    if (m_sock) {
        m_sock->disconnect(this);
        m_sock->abort();
        m_sock->deleteLater();
    }
    m_sock = new QLocalSocket(this);
    m_buf.clear();
    m_skipLine = false;
    connect(m_sock, &QLocalSocket::connected, this, [this] {
        m_lastError.clear();
        m_wasConnected = true;
        emit connectedChanged(true);
    });
    connect(m_sock, &QLocalSocket::readyRead, this, &DaemonClient::onReadyRead);
    connect(m_sock, &QLocalSocket::disconnected, this, &DaemonClient::onDisconnected);
    connect(m_sock, &QLocalSocket::errorOccurred, this, [this](QLocalSocket::LocalSocketError e) {
        if (isConnected())
            return;
        const QString text = describe(e, m_path, m_sock->errorString());
        if (text != m_lastError) {
            m_lastError = text;
            emit lastConnectErrorChanged();
        }
    });
    m_sock->connectToServer(m_path);
}

void DaemonClient::onDisconnected()
{
    failPending(tr("daemon connection lost"));
    if (m_wasConnected) {
        m_wasConnected = false;
        m_lastError = tr("daemon connection lost");
        emit connectedChanged(false);
    }
}

void DaemonClient::failPending(const QString &why)
{
    const auto pending = std::exchange(m_pending, {});
    for (const Done &d : pending)
        d(false, why, QJsonValue());
}

void DaemonClient::onReadyRead()
{
    m_buf += m_sock->readAll();
    int nl;
    while ((nl = m_buf.indexOf('\n')) >= 0) {
        const QByteArray line = m_buf.left(nl);
        m_buf.remove(0, nl + 1);
        if (m_skipLine) { // the tail of an oversized line
            m_skipLine = false;
            continue;
        }
        const ygg::Frame f = ygg::parseFrame(line);
        if (f.kind == ygg::Frame::Response) {
            if (Done d = m_pending.take(f.id))
                d(f.ok, f.error, f.data);
        } else if (f.kind == ygg::Frame::Event) {
            emit eventReceived(f.event, f.data);
        }
    }
    if (m_buf.size() > ygg::kMaxLine) { // no newline within the limit: drop what we have, skip to the next newline
        m_buf.clear();
        m_skipLine = true;
    }
}

void DaemonClient::call(const QString &cmd, const QJsonObject &args, Done done)
{
    if (!isConnected()) {
        const QString why = m_lastError.isEmpty() ? tr("daemon not reachable") : tr("daemon not reachable: %1").arg(m_lastError);
        QTimer::singleShot(0, this, [done = std::move(done), why] { done(false, why, QJsonValue()); });
        return;
    }
    const int id = ++m_nextId;
    m_pending.insert(id, std::move(done));
    m_sock->write(ygg::encodeRequest(id, cmd, args));
}
