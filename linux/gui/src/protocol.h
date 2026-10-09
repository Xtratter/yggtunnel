#pragma once

#include <QByteArray>
#include <QJsonObject>
#include <QJsonValue>
#include <QString>

// The daemon's wire format: one JSON object per line (see linux/internal/ipc).
namespace ygg {

constexpr int kMaxLine = 1 << 20;

// One request line, '\n' terminated.
QByteArray encodeRequest(int id, const QString &cmd, const QJsonObject &args = {});

struct Frame {
    enum Kind { Response, Event, Invalid } kind = Invalid;
    int id = 0;
    bool ok = false;
    QString error;
    QJsonValue data;
    QString event;
};

// Response if the object has "id", Event if it has "event", otherwise Invalid.
Frame parseFrame(const QByteArray &line);

} // namespace ygg
