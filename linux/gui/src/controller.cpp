#include "controller.h"

#include <QDateTime>
#include <QFile>
#include <QJsonArray>
#include <QJsonObject>

#include "daemonclient.h"

namespace {
constexpr int kHistoryMax = 200;
constexpr qint64 kImportMaxBytes = 64 * 1024;
}

Controller::Controller(DaemonClient *client, QObject *parent) : QObject(parent), m_client(client)
{
    m_poll.setInterval(2000);
    connect(&m_poll, &QTimer::timeout, this, &Controller::pollStatus);
    connect(client, &DaemonClient::connectedChanged, this, &Controller::onConnectedChanged);
    connect(client, &DaemonClient::eventReceived, this, &Controller::onEvent);
    connect(client, &DaemonClient::lastConnectErrorChanged, this, [this] {
        if (!m_reachable) { // the reason shown while the daemon cannot be reached
            setState("unreachable", m_client->lastConnectError());
            emit changed();
        }
    });
    if (client->isConnected())
        onConnectedChanged(true);
}

QString Controller::reachError() const
{
    return m_reachable ? QString() : m_client->lastConnectError();
}

void Controller::setState(const QString &state, const QString &error)
{
    m_state = state;
    const QString key = state + '\n' + error;
    if (key != m_lastHistoryKey) {
        m_lastHistoryKey = key;
        QString line = QDateTime::currentDateTime().toString("HH:mm:ss") + ' ' + state;
        if (!error.isEmpty())
            line += ": " + error;
        m_history.prepend(line);
        while (m_history.size() > kHistoryMax)
            m_history.removeLast();
        emit historyChanged();
    }
}

void Controller::onConnectedChanged(bool connected)
{
    m_reachable = connected;
    m_actionError.clear();
    if (connected) {
        m_poll.start();
        pollStatus();
    } else {
        m_poll.stop();
        m_busy = false;
        m_polling = false;
        m_peers.clear();
        m_handshakeAgo = -1;
        m_ksActive = false; // nobody can vouch for it any more
        m_splitApplied.clear();
        m_splitResolved = 0;
        m_splitResolveError.clear();
        setState("unreachable", m_client->lastConnectError());
    }
    emit changed();
}

// The daemon answers one connection's commands in order, so `status` waits behind a running `up`;
// the state then comes from the pushed events.
void Controller::onEvent(const QString &kind, const QJsonValue &data)
{
    if (kind != "state" || !data.isObject())
        return;
    const QJsonObject o = data.toObject();
    const QString state = o["state"].toString();
    if (state.isEmpty())
        return;
    if (!o["error"].toString().isEmpty())
        m_statusError = o["error"].toString();
    if (state == "connected")
        m_actionError.clear();
    setState(state, o["error"].toString());
    emit changed();
}

void Controller::pollStatus()
{
    if (!m_reachable || m_polling || m_busy)
        return;
    m_polling = true;
    m_client->call("status", {}, [this](bool ok, const QString &error, const QJsonValue &data) {
        m_polling = false;
        if (!ok) { // not a state: the connection is failing; the unreachable path reports it
            emit changed();
            return;
        }
        applyStatus(data.toObject());
    });
}

void Controller::applyStatus(const QJsonObject &s)
{
    const QJsonObject profile = s["profile"].toObject();
    m_profileName = profile["name"].toString();
    m_serverAddress = profile["serverYgg"].toString(); // the private key in `profile` is deliberately not read
    const QString err = s["error"].toString();
    m_statusError = err; // an empty answer clears an old failure
    const QJsonObject node = s["node"].toObject();
    const QJsonObject settings = s["settings"].toObject();
    m_killSwitch = settings["killSwitch"].toBool(false);
    m_allowLan = settings["allowLan"].toBool(true);
    m_ksActive = s["killSwitchActive"].toBool(false);
    const QJsonObject split = settings["split"].toObject();
    m_splitMode = split["mode"].toString("all");
    m_splitSubnets.clear();
    for (const QJsonValue &v : split["subnets"].toArray())
        m_splitSubnets << v.toString();
    m_splitDomains.clear();
    for (const QJsonValue &v : split["domains"].toArray())
        m_splitDomains << v.toString();
    const QJsonObject splitStatus = s["splitStatus"].toObject();
    m_splitApplied = splitStatus["mode"].toString();
    m_splitResolved = splitStatus["resolved"].toInt();
    m_splitResolveError = splitStatus["resolveError"].toString();
    m_nodeAddress = node["address"].toString();
    m_handshakeAgo = node["tunnel"].toObject()["handshakeAgo"].toDouble(-1);
    m_peers.clear();
    for (const QJsonValue &v : node["peers"].toArray()) {
        const QJsonObject p = v.toObject();
        m_peers << QVariantMap{{"uri", p["uri"].toString()},
                               {"up", p["up"].toBool()},
                               {"latencyMs", p["latencyMs"].toDouble()},
                               {"rx", p["rx"].toDouble()},
                               {"tx", p["tx"].toDouble()},
                               {"error", p["error"].toString()}};
    }
    setState(s["state"].toString("off"), err);
    emit changed();
}

void Controller::runAction(const QString &cmd)
{
    if (!m_reachable || m_busy)
        return;
    m_busy = true;
    m_actionError.clear();
    emit changed();
    m_client->call(cmd, {}, [this](bool ok, const QString &error, const QJsonValue &) {
        m_busy = false;
        m_actionError = ok ? QString() : error;
        emit changed();
        pollStatus();
    });
}

void Controller::sendSettings(const QJsonObject &args)
{
    if (!m_reachable || m_busy)
        return;
    m_busy = true;
    m_actionError.clear();
    emit changed();
    m_client->call("set", args, [this](bool ok, const QString &error, const QJsonValue &) {
        m_busy = false;
        m_actionError = ok ? QString() : error;
        emit changed();
        pollStatus(); // the settings and "active" shown are whatever the daemon reports
    });
}

void Controller::setSplit(const QString &mode, const QStringList &subnets, const QStringList &domains)
{
    sendSettings(QJsonObject{{"split", QJsonObject{{"mode", mode},
                                                   {"subnets", QJsonArray::fromStringList(subnets)},
                                                   {"domains", QJsonArray::fromStringList(domains)}}}});
}

void Controller::setKillSwitch(bool on) { sendSettings(QJsonObject{{"killSwitch", on}}); }
void Controller::setAllowLan(bool allow) { sendSettings(QJsonObject{{"allowLan", allow}}); }

void Controller::up() { runAction("up"); }
void Controller::down() { runAction("down"); }
void Controller::panic() { runAction("panic"); }

void Controller::importLink(const QString &text)
{
    if (text.toUtf8().size() > kImportMaxBytes) { // the daemon drops a connection that sends a huge line
        emit importFinished(false, tr("The profile is too large (over 64 KiB)."));
        return;
    }
    if (text.trimmed().isEmpty()) {
        emit importFinished(false, tr("Paste a yggtunnel://import#… link first."));
        return;
    }
    if (!m_reachable) {
        emit importFinished(false, tr("The daemon is not reachable."));
        return;
    }
    m_client->call("import", QJsonObject{{"link", text}}, [this](bool ok, const QString &error, const QJsonValue &) {
        emit importFinished(ok, ok ? QString() : error);
        if (ok)
            pollStatus();
    });
}

void Controller::importFile(const QUrl &url)
{
    QFile f(url.toLocalFile());
    if (!f.open(QIODevice::ReadOnly)) {
        emit importFinished(false, tr("Cannot read the file: %1").arg(f.errorString()));
        return;
    }
    // read() with a limit: size() is 0 for pipes and devices, and readAll() on them never ends
    const QByteArray data = f.read(kImportMaxBytes + 1);
    if (data.size() > kImportMaxBytes) {
        emit importFinished(false, tr("The file is too large for a profile (over 64 KiB)."));
        return;
    }
    importLink(QString::fromUtf8(data));
}

void Controller::refreshLog()
{
    if (!m_reachable)
        return;
    m_client->call("log", {}, [this](bool ok, const QString &, const QJsonValue &data) {
        if (!ok)
            return;
        const QString text = data.toString();
        if (text != m_nodeLog) {
            m_nodeLog = text;
            emit logChanged();
        }
    });
}
