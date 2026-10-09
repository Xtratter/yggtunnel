#include "protocol.h"

#include <QJsonDocument>

namespace ygg {

QByteArray encodeRequest(int id, const QString &cmd, const QJsonObject &args)
{
    QJsonObject o{{"id", id}, {"cmd", cmd}};
    if (!args.isEmpty())
        o.insert("args", args);
    return QJsonDocument(o).toJson(QJsonDocument::Compact) + '\n';
}

Frame parseFrame(const QByteArray &line)
{
    Frame f;
    if (line.isEmpty() || line.size() > kMaxLine)
        return f;
    QJsonParseError err;
    const QJsonDocument doc = QJsonDocument::fromJson(line, &err);
    if (err.error != QJsonParseError::NoError || !doc.isObject())
        return f;
    const QJsonObject o = doc.object();
    if (o.contains("id")) {
        if (!o["id"].isDouble())
            return f;
        f.kind = Frame::Response;
        f.id = o["id"].toInt();
        f.ok = o["ok"].toBool();
        f.error = o["error"].toString();
        f.data = o["data"];
    } else if (o["event"].isString()) {
        f.kind = Frame::Event;
        f.event = o["event"].toString();
        f.data = o["data"];
    }
    return f;
}

} // namespace ygg
