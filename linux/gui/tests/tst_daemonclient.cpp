#include <QtTest>
#include <QJsonObject>
#include <QLocalSocket>
#include <QSignalSpy>
#include <QTemporaryDir>
#include <memory>

#include "daemonclient.h"
#include "fakedaemon.h"
#include "protocol.h"

struct Result {
    bool done = false;
    bool ok = false;
    QString error;
    QJsonValue data;
    int times = 0;
};

static DaemonClient::Done into(Result *r)
{
    return [r](bool ok, const QString &error, const QJsonValue &data) {
        r->done = true;
        r->ok = ok;
        r->error = error;
        r->data = data;
        r->times++;
    };
}

class TstDaemonClient : public QObject {
    Q_OBJECT
    QTemporaryDir dir;
    QString sock() const { return dir.filePath("d.sock"); }

private slots:
    void callRoundTrip() {
        FakeDaemon d;
        d.handler = [](const QString &cmd, const QJsonObject &, bool *, QString *) { return QJsonObject{{"echo", cmd}}; };
        QVERIFY(d.listen(sock()));
        DaemonClient c(sock());
        c.setRetryInterval(100);
        c.start();
        QTRY_VERIFY(c.isConnected());
        Result r;
        c.call("status", {}, into(&r));
        QTRY_VERIFY(r.done);
        QVERIFY(r.ok);
        QCOMPARE(r.data.toObject()["echo"].toString(), QString("status"));
        QCOMPARE(r.times, 1);
    }
    void argsReachTheDaemon() {
        FakeDaemon d;
        QVERIFY(d.listen(sock()));
        DaemonClient c(sock());
        c.start();
        QTRY_VERIFY(c.isConnected());
        Result r;
        c.call("import", QJsonObject{{"link", "x"}}, into(&r));
        QTRY_VERIFY(r.done);
        QCOMPARE(d.receivedArgs.last()["link"].toString(), QString("x"));
    }
    void errorResponseIsReported() {
        FakeDaemon d;
        d.handler = [](const QString &, const QJsonObject &, bool *ok, QString *e) { *ok = false; *e = "already in progress"; return QJsonValue(); };
        QVERIFY(d.listen(sock()));
        DaemonClient c(sock());
        c.start();
        QTRY_VERIFY(c.isConnected());
        Result r;
        c.call("up", {}, into(&r));
        QTRY_VERIFY(r.done);
        QVERIFY(!r.ok);
        QCOMPARE(r.error, QString("already in progress"));
    }
    void eventsAreDelivered() {
        FakeDaemon d;
        QVERIFY(d.listen(sock()));
        DaemonClient c(sock());
        c.start();
        QTRY_VERIFY(c.isConnected());
        QSignalSpy spy(&c, &DaemonClient::eventReceived);
        QTRY_COMPARE(d.connections(), 1); // the server side has accepted the client
        d.pushEvent("state", QJsonObject{{"state", "connected"}});
        QTRY_COMPARE(spy.count(), 1);
        QCOMPARE(spy.at(0).at(0).toString(), QString("state"));
        QCOMPARE(spy.at(0).at(1).toJsonValue().toObject()["state"].toString(), QString("connected"));
    }
    void pendingCallsFailWhenConnectionDrops() {
        FakeDaemon d;
        d.silent = true;
        QVERIFY(d.listen(sock()));
        DaemonClient c(sock());
        c.setRetryInterval(5000);
        c.start();
        QTRY_VERIFY(c.isConnected());
        Result r;
        c.call("up", {}, into(&r));
        QTRY_VERIFY(!d.received.isEmpty());
        d.dropClients();
        QTRY_VERIFY(r.done);
        QVERIFY(!r.ok);
        QVERIFY(r.error.contains("lost"));
        QCOMPARE(r.times, 1);
    }
    void reconnectsWhenDaemonAppearsLater() {
        DaemonClient c(sock());
        c.setRetryInterval(100);
        c.start();
        QTest::qWait(250);
        QVERIFY(!c.isConnected());
        QVERIFY2(c.lastConnectError().contains("not found"), qPrintable(c.lastConnectError()));
        FakeDaemon d;
        QVERIFY(d.listen(sock()));
        QTRY_VERIFY_WITH_TIMEOUT(c.isConnected(), 5000);
        QVERIFY(c.lastConnectError().isEmpty());
    }
    void reconnectsAfterDaemonRestart() {
        FakeDaemon d;
        QVERIFY(d.listen(sock()));
        DaemonClient c(sock());
        c.setRetryInterval(100);
        c.start();
        QTRY_VERIFY(c.isConnected());
        QSignalSpy spy(&c, &DaemonClient::connectedChanged);
        d.close();
        QTRY_VERIFY(!c.isConnected());
        QVERIFY(d.listen(sock()));
        QTRY_VERIFY_WITH_TIMEOUT(c.isConnected(), 5000);
        QVERIFY(spy.count() >= 2);
    }
    void permissionDeniedGivesReadableError() {
        FakeDaemon d;
        QVERIFY(d.listen(sock()));
        QVERIFY(QFile::setPermissions(sock(), QFileDevice::Permissions()));
        DaemonClient c(sock());
        c.setRetryInterval(100);
        c.start();
        QTRY_VERIFY(!c.lastConnectError().isEmpty());
        QVERIFY2(c.lastConnectError().contains("permission", Qt::CaseInsensitive), qPrintable(c.lastConnectError()));
        QVERIFY(!c.isConnected());
    }
    void garbageLineIsIgnored() {
        FakeDaemon d;
        QVERIFY(d.listen(sock()));
        DaemonClient c(sock());
        c.start();
        QTRY_VERIFY(c.isConnected());
        QTRY_COMPARE(d.connections(), 1);
        d.push("this is not json");
        d.push("{\"event\":\"state\"}"); // valid event without data
        d.push("[1,2,3]");
        Result r;
        c.call("status", {}, into(&r));
        QTRY_VERIFY(r.done);
        QVERIFY(r.ok);
    }
    void oversizedLineDoesNotBreakTheClient() {
        FakeDaemon d;
        QVERIFY(d.listen(sock()));
        DaemonClient c(sock());
        c.start();
        QTRY_VERIFY(c.isConnected());
        QTRY_COMPARE(d.connections(), 1);
        d.push(QByteArray(ygg::kMaxLine + 100, 'x'));
        Result r;
        c.call("status", {}, into(&r));
        QTRY_VERIFY_WITH_TIMEOUT(r.done, 5000);
        QVERIFY(r.ok);
        QVERIFY(c.isConnected());
    }
    void callWhileDisconnectedFailsImmediately() {
        DaemonClient c(sock());
        Result r;
        c.call("up", {}, into(&r));
        QTRY_VERIFY(r.done);
        QVERIFY(!r.ok);
        QVERIFY(!r.error.isEmpty());
        QCOMPARE(r.times, 1);
    }
};

QTEST_MAIN(TstDaemonClient)
#include "tst_daemonclient.moc"
