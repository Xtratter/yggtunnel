#include <QtTest>
#include <QJsonDocument>
#include <QJsonObject>

#include "protocol.h"

// moc cannot parse raw string literals, so test JSON is written with single quotes.
static QByteArray j(const char *s) { return QByteArray(s).replace('\'', '"'); }

class TstProtocol : public QObject {
    Q_OBJECT
private slots:
    void encodeRequestIsOneLine() {
        const QByteArray b = ygg::encodeRequest(3, "up");
        QVERIFY(b.endsWith('\n'));
        QCOMPARE(b.count('\n'), 1);
        const QJsonObject o = QJsonDocument::fromJson(b).object();
        QCOMPARE(o["id"].toInt(), 3);
        QCOMPARE(o["cmd"].toString(), QString("up"));
        QVERIFY(!o.contains("args"));
    }
    void encodeRequestKeepsArgs() {
        const QByteArray b = ygg::encodeRequest(1, "import", QJsonObject{{"link", "yggtunnel://import#x\ny"}});
        QCOMPARE(b.count('\n'), 1); // a newline inside a value must be escaped
        QCOMPARE(QJsonDocument::fromJson(b).object()["args"].toObject()["link"].toString(), QString("yggtunnel://import#x\ny"));
    }
    void parsesResponseOk() {
        const auto f = ygg::parseFrame(j("{'id':7,'ok':true,'data':{'state':'off'}}"));
        QCOMPARE(f.kind, ygg::Frame::Response);
        QCOMPARE(f.id, 7);
        QVERIFY(f.ok);
        QCOMPARE(f.data.toObject()["state"].toString(), QString("off"));
    }
    void parsesResponseError() {
        const auto f = ygg::parseFrame(j("{'id':2,'ok':false,'error':'already connected'}"));
        QCOMPARE(f.kind, ygg::Frame::Response);
        QVERIFY(!f.ok);
        QCOMPARE(f.error, QString("already connected"));
    }
    void parsesEvent() {
        const auto f = ygg::parseFrame(j("{'event':'state','data':{'state':'connected'}}"));
        QCOMPARE(f.kind, ygg::Frame::Event);
        QCOMPARE(f.event, QString("state"));
        QCOMPARE(f.data.toObject()["state"].toString(), QString("connected"));
    }
    void rejectsGarbage_data() {
        QTest::addColumn<QByteArray>("line");
        QTest::newRow("text") << QByteArray("not json");
        QTest::newRow("array") << QByteArray("[1,2]");
        QTest::newRow("empty object") << QByteArray("{}");
        QTest::newRow("empty") << QByteArray();
        QTest::newRow("truncated") << QByteArray(j("{'id':1,'ok':tr"));
        QTest::newRow("id wrong type") << QByteArray(j("{'id':'x','ok':true}"));
    }
    void rejectsGarbage() {
        QFETCH(QByteArray, line);
        QCOMPARE(ygg::parseFrame(line).kind, ygg::Frame::Invalid);
    }
    void rejectsOversizedLine() {
        QByteArray big = j("{'id':1,'ok':true,'data':'") + QByteArray(ygg::kMaxLine, 'a') + j("'}");
        QCOMPARE(ygg::parseFrame(big).kind, ygg::Frame::Invalid);
    }
};

QTEST_APPLESS_MAIN(TstProtocol)
#include "tst_protocol.moc"
