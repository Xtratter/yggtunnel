#include <QtTest>
#include <QJsonArray>
#include <QJsonObject>
#include <QSignalSpy>
#include <QTemporaryDir>
#include <QTemporaryFile>

#include "controller.h"
#include "daemonclient.h"
#include "fakedaemon.h"

static QJsonObject statusData(const QString &state, bool withNode = false, const QString &error = {})
{
    QJsonObject o{{"state", state},
                  {"profile", QJsonObject{{"name", "example"}, {"serverYgg", "200:db8::2"}, {"privateKey", "…abcd"}}}};
    if (!error.isEmpty())
        o["error"] = error;
    if (withNode) {
        QJsonArray peers;
        peers << QJsonObject{{"uri", "tls://192.0.2.1:1234"}, {"up", true}, {"latencyMs", 42.5}, {"rx", 10}, {"tx", 20}}
              << QJsonObject{{"uri", "tls://192.0.2.2:1234"}, {"up", false}, {"error", "connection refused"}}
              << QJsonObject{{"uri", "wss://192.0.2.3:443"}, {"up", true}, {"latencyMs", 80}};
        o["node"] = QJsonObject{{"running", true}, {"address", "200:db8::1"}, {"peers", peers},
                                {"tunnel", QJsonObject{{"handshakeAgo", 12}, {"rx", 1}, {"tx", 2}}}};
    }
    return o;
}

class TstController : public QObject {
    Q_OBJECT
    QTemporaryDir dir;
    QString sock() const { return dir.filePath("d.sock"); }

    struct Rig {
        FakeDaemon daemon;
        DaemonClient client;
        Controller ctl;
        QString state = "off";
        QJsonObject settings{{"killSwitch", false}, {"allowLan", true}};
        bool ksActive = false;
        QJsonObject splitStatus;
        Rig(const QString &path) : client(path), ctl(&client) {
            client.setRetryInterval(100);
            ctl.setPollInterval(50);
            daemon.handler = [this](const QString &cmd, const QJsonObject &, bool *, QString *) -> QJsonValue {
                if (cmd == "status") {
                    QJsonObject o = statusData(state, state == "connected");
                    o["settings"] = settings;
                    o["killSwitchActive"] = ksActive;
                    if (!splitStatus.isEmpty())
                        o["splitStatus"] = splitStatus;
                    return o;
                }
                if (cmd == "log")
                    return QString("node log line");
                return QJsonValue();
            };
        }
    };

private slots:
    void statusFillsProperties() {
        Rig r(sock());
        r.state = "connected";
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("connected"));
        QVERIFY(r.ctl.daemonReachable());
        QVERIFY(r.ctl.hasProfile());
        QCOMPARE(r.ctl.profileName(), QString("example"));
        QCOMPARE(r.ctl.serverAddress(), QString("200:db8::2"));
        QCOMPARE(r.ctl.nodeAddress(), QString("200:db8::1"));
        QCOMPARE(r.ctl.handshakeAgo(), 12.0);
        const QVariantList peers = r.ctl.peers();
        QCOMPARE(peers.size(), 3);
        QCOMPARE(peers[0].toMap()["uri"].toString(), QString("tls://192.0.2.1:1234"));
        QCOMPARE(peers[0].toMap()["latencyMs"].toDouble(), 42.5);
        QCOMPARE(peers[1].toMap()["up"].toBool(), false);
        QCOMPARE(peers[1].toMap()["error"].toString(), QString("connection refused"));
    }
    void stateEventUpdatesImmediately() {
        Rig r(sock());
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        QTRY_COMPARE(r.daemon.connections(), 1);
        r.ctl.setPollInterval(60000); // only the event can change the state now
        r.daemon.pushEvent("state", QJsonObject{{"state", "starting"}});
        QTRY_COMPARE(r.ctl.state(), QString("starting"));
    }
    void unreachableWhenNoDaemon() {
        Rig r(sock());
        r.client.start();
        QTest::qWait(250);
        QCOMPARE(r.ctl.state(), QString("unreachable"));
        QVERIFY(!r.ctl.daemonReachable());
        QVERIFY(!r.ctl.reachError().isEmpty());
        r.ctl.up();
        QVERIFY(!r.ctl.busy()); // nothing to wait for
    }
    void unreachableErrorAppearsWithoutAnyOtherChange() {
        Rig r(sock());
        QSignalSpy spy(&r.ctl, &Controller::changed);
        r.client.start();
        QTRY_VERIFY(!r.ctl.reachError().isEmpty());
        QVERIFY(spy.count() >= 1); // QML bindings get told
        QVERIFY(r.ctl.history().first().contains("unreachable"));
    }
    void recoversWhenDaemonAppears() {
        Rig r(sock());
        r.client.start();
        QTest::qWait(250);
        QCOMPARE(r.ctl.state(), QString("unreachable"));
        QVERIFY(r.daemon.listen(sock()));
        QTRY_COMPARE_WITH_TIMEOUT(r.ctl.state(), QString("off"), 5000);
        QVERIFY(r.ctl.daemonReachable());
        QVERIFY(r.ctl.reachError().isEmpty());
    }
    void upSetsBusyAndClearsIt() {
        Rig r(sock());
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        r.ctl.up();
        QVERIFY(r.ctl.busy());
        QTRY_VERIFY(!r.ctl.busy());
        QVERIFY(r.daemon.received.contains("up"));
    }
    void secondUpWhileBusyIsIgnored() {
        Rig r(sock());
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        r.ctl.up();
        r.ctl.up();
        r.ctl.up();
        QTRY_VERIFY(!r.ctl.busy());
        QTest::qWait(100);
        QCOMPARE(r.daemon.received.count("up"), 1);
    }
    void daemonErrorIsShown() {
        Rig r(sock());
        auto base = r.daemon.handler;
        r.daemon.handler = [base](const QString &cmd, const QJsonObject &a, bool *ok, QString *e) -> QJsonValue {
            if (cmd == "up") { *ok = false; *e = "already in progress"; return QJsonValue(); }
            return base(cmd, a, ok, e);
        };
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        r.ctl.up();
        QTRY_COMPARE(r.ctl.lastError(), QString("already in progress"));
        QVERIFY(!r.ctl.busy());
    }
    void staleActionErrorClearsAfterReconnect() {
        Rig r(sock());
        auto base = r.daemon.handler;
        r.daemon.handler = [base](const QString &cmd, const QJsonObject &a, bool *ok, QString *e) -> QJsonValue {
            if (cmd == "up") { *ok = false; *e = "step failed"; return QJsonValue(); }
            return base(cmd, a, ok, e);
        };
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        r.ctl.up();
        QTRY_COMPARE(r.ctl.lastError(), QString("step failed"));
        r.daemon.close(); // the daemon restarts
        QTRY_COMPARE(r.ctl.state(), QString("unreachable"));
        QVERIFY(r.daemon.listen(sock()));
        QTRY_COMPARE_WITH_TIMEOUT(r.ctl.state(), QString("off"), 5000);
        QCOMPARE(r.ctl.lastError(), QString());
    }
    void statusErrorDisappearsWhenStatusHasNone() {
        Rig r(sock());
        QString err = "old failure";
        r.daemon.handler = [&](const QString &cmd, const QJsonObject &, bool *, QString *) -> QJsonValue {
            return cmd == "status" ? QJsonValue(statusData("off", false, err)) : QJsonValue();
        };
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.lastError(), QString("old failure"));
        err.clear();
        QTRY_COMPARE(r.ctl.lastError(), QString());
    }
    void importLinkRefusesHugeText() {
        Rig r(sock());
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        QSignalSpy spy(&r.ctl, &Controller::importFinished);
        r.ctl.importLink(QString(70 * 1024, 'x'));
        QTRY_COMPARE(spy.count(), 1);
        QCOMPARE(spy.at(0).at(0).toBool(), false);
        QVERIFY(!r.daemon.received.contains("import")); // a huge line would make the daemon drop the connection
    }
    void settingsAreReadFromStatus() {
        Rig r(sock());
        r.settings = QJsonObject{{"killSwitch", true}, {"allowLan", false}};
        r.ksActive = true;
        r.state = "connected";
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_VERIFY(r.ctl.killSwitch());
        QVERIFY(!r.ctl.allowLan());
        QVERIFY(r.ctl.killSwitchActive());
    }
    void settingsDefaultsBeforeAnyStatus() {
        Rig r(sock());
        QVERIFY(!r.ctl.killSwitch());
        QVERIFY(r.ctl.allowLan());
        QVERIFY(!r.ctl.killSwitchActive());
    }
    void setKillSwitchSendsSetAndDisablesWhileInFlight() {
        Rig r(sock());
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        QTRY_COMPARE(r.daemon.connections(), 1);
        r.daemon.silent = true; // the answer never comes: the call stays in flight
        r.ctl.setKillSwitch(true);
        QVERIFY(r.ctl.busy());
        QTRY_VERIFY(r.daemon.received.contains("set"));
        QCOMPARE(r.daemon.receivedArgs.at(r.daemon.received.indexOf("set")), (QJsonObject{{"killSwitch", true}}));
        r.ctl.setAllowLan(false); // ignored while busy
        QTest::qWait(100);
        QCOMPARE(r.daemon.received.count("set"), 1);
    }
    void setAllowLanSendsOnlyThatField() {
        Rig r(sock());
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        r.ctl.setAllowLan(false);
        QTRY_VERIFY(r.daemon.received.contains("set"));
        QCOMPARE(r.daemon.receivedArgs.at(r.daemon.received.indexOf("set")), (QJsonObject{{"allowLan", false}}));
    }
    void killSwitchActiveComesOnlyFromTheDaemon() {
        Rig r(sock());
        r.settings = QJsonObject{{"killSwitch", false}, {"allowLan", true}};
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        r.ctl.setKillSwitch(true); // the fake answers ok but its status keeps saying "not active"
        QTRY_VERIFY(!r.ctl.busy());
        QTest::qWait(200);
        QVERIFY(!r.ctl.killSwitchActive());
        QVERIFY(!r.ctl.killSwitch()); // and the stored setting is whatever the daemon reports
    }
    void setErrorIsShown() {
        Rig r(sock());
        auto base = r.daemon.handler;
        r.daemon.handler = [base](const QString &cmd, const QJsonObject &a, bool *ok, QString *e) -> QJsonValue {
            if (cmd == "set") { *ok = false; *e = "not authorized by polkit"; return QJsonValue(); }
            return base(cmd, a, ok, e);
        };
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        r.ctl.setKillSwitch(true);
        QTRY_COMPARE(r.ctl.lastError(), QString("not authorized by polkit"));
        QVERIFY(!r.ctl.busy());
    }
    void splitFieldsAreReadFromStatus() {
        Rig r(sock());
        r.settings = QJsonObject{{"killSwitch", false}, {"allowLan", true},
                                 {"split", QJsonObject{{"mode", "exclude"}, {"subnets", QJsonArray{"203.0.113.0/24", "198.51.100.0/24"}},
                                                       {"domains", QJsonArray{"example.com"}}}}};
        r.splitStatus = QJsonObject{{"mode", "exclude"}, {"resolved", 4}, {"resolveError", "example.org: servfail"}};
        r.state = "connected";
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.splitMode(), QString("exclude"));
        QCOMPARE(r.ctl.splitSubnets(), (QStringList{"203.0.113.0/24", "198.51.100.0/24"}));
        QCOMPARE(r.ctl.splitDomains(), QStringList{"example.com"});
        QCOMPARE(r.ctl.splitResolved(), 4);
        QCOMPARE(r.ctl.splitResolveError(), QString("example.org: servfail"));
        QCOMPARE(r.ctl.splitApplied(), QString("exclude"));
    }
    void splitDefaultsBeforeAnyStatus() {
        Rig r(sock());
        QCOMPARE(r.ctl.splitMode(), QString("all"));
        QVERIFY(r.ctl.splitSubnets().isEmpty() && r.ctl.splitDomains().isEmpty());
        QCOMPARE(r.ctl.splitApplied(), QString());
    }
    void setSplitSendsOneSetWithTheWholeObject() {
        Rig r(sock());
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        r.ctl.setSplit("only", {"203.0.113.0/24"}, {"example.com", "example.net"});
        QTRY_VERIFY(r.daemon.received.contains("set"));
        const QJsonObject a = r.daemon.receivedArgs.at(r.daemon.received.indexOf("set"));
        QCOMPARE(a.keys(), QStringList{"split"});
        const QJsonObject s = a["split"].toObject();
        QCOMPARE(s["mode"].toString(), QString("only"));
        QCOMPARE(s["subnets"].toArray().size(), 1);
        QCOMPARE(s["domains"].toArray().size(), 2);
    }
    void setSplitIgnoredWhileBusy() {
        Rig r(sock());
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        QTRY_COMPARE(r.daemon.connections(), 1);
        r.daemon.silent = true;
        r.ctl.setSplit("only", {}, {});
        QVERIFY(r.ctl.busy());
        r.ctl.setSplit("exclude", {}, {});
        QTest::qWait(100);
        QCOMPARE(r.daemon.received.count("set"), 1);
    }
    void splitErrorIsShown() {
        Rig r(sock());
        auto base = r.daemon.handler;
        r.daemon.handler = [base](const QString &cmd, const QJsonObject &a, bool *ok, QString *e) -> QJsonValue {
            if (cmd == "set") { *ok = false; *e = "disconnect first to change the routing mode"; return QJsonValue(); }
            return base(cmd, a, ok, e);
        };
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        r.ctl.setSplit("only", {}, {});
        QTRY_COMPARE(r.ctl.lastError(), QString("disconnect first to change the routing mode"));
        QVERIFY(!r.ctl.busy());
    }
    void historyKeepsNewestFirstAndCaps200() {
        Rig r(sock());
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        QTRY_COMPARE(r.daemon.connections(), 1);
        r.ctl.setPollInterval(60000);
        for (int i = 0; i < 250; ++i)
            r.daemon.pushEvent("state", QJsonObject{{"state", i % 2 ? "starting" : "connected"}});
        r.daemon.pushEvent("state", QJsonObject{{"state", "error"}, {"error", "boom"}});
        QTRY_COMPARE(r.ctl.state(), QString("error"));
        QCOMPARE(r.ctl.history().size(), 200);
        QVERIFY2(r.ctl.history().first().contains("error: boom"), qPrintable(r.ctl.history().first()));
        QVERIFY(QRegularExpression("^\\d\\d:\\d\\d:\\d\\d ").match(r.ctl.history().first()).hasMatch());
    }
    void repeatedIdenticalStatesAreNotRepeatedInHistory() {
        Rig r(sock());
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        QTest::qWait(300); // several polls with the same answer
        QCOMPARE(r.ctl.history().size(), 1);
    }
    void privateKeyNeverExposed() {
        Rig r(sock());
        r.state = "connected";
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("connected"));
        r.ctl.refreshLog();
        QTRY_COMPARE(r.ctl.nodeLog(), QString("node log line"));
        QStringList all = r.ctl.history();
        all << r.ctl.profileName() << r.ctl.serverAddress() << r.ctl.nodeAddress() << r.ctl.lastError() << r.ctl.nodeLog();
        for (const QVariant &p : r.ctl.peers())
            all << QString::fromUtf8(QJsonDocument(QJsonObject::fromVariantMap(p.toMap())).toJson());
        QVERIFY(!all.join('\n').contains("abcd"));
        QVERIFY(!QString(r.ctl.metaObject()->className()).isEmpty());
    }
    void importFileRefusesHugeFile() {
        Rig r(sock());
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        QTemporaryFile f;
        QVERIFY(f.open());
        f.write(QByteArray(70 * 1024, 'x'));
        f.flush();
        QSignalSpy spy(&r.ctl, &Controller::importFinished);
        r.ctl.importFile(QUrl::fromLocalFile(f.fileName()));
        QTRY_COMPARE(spy.count(), 1);
        QCOMPARE(spy.at(0).at(0).toBool(), false);
        QVERIFY(!spy.at(0).at(1).toString().isEmpty());
        QVERIFY(!r.daemon.received.contains("import"));
    }
    void importFileMissingReportsError() {
        Rig r(sock());
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        QSignalSpy spy(&r.ctl, &Controller::importFinished);
        r.ctl.importFile(QUrl::fromLocalFile(dir.filePath("nope.txt")));
        QTRY_COMPARE(spy.count(), 1);
        QCOMPARE(spy.at(0).at(0).toBool(), false);
    }
    void importFileSendsContent() {
        Rig r(sock());
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        QTemporaryFile f;
        QVERIFY(f.open());
        f.write("yggtunnel://import#abc\n");
        f.flush();
        QSignalSpy spy(&r.ctl, &Controller::importFinished);
        r.ctl.importFile(QUrl::fromLocalFile(f.fileName()));
        QTRY_COMPARE(spy.count(), 1);
        QCOMPARE(spy.at(0).at(0).toBool(), true);
        const int i = r.daemon.received.indexOf("import"); // later status polls come after it
        QVERIFY(i >= 0);
        QCOMPARE(r.daemon.receivedArgs.at(i)["link"].toString(), QString("yggtunnel://import#abc\n"));
    }
    void importLinkReportsDaemonError() {
        Rig r(sock());
        r.daemon.handler = [](const QString &cmd, const QJsonObject &, bool *ok, QString *e) -> QJsonValue {
            if (cmd == "import") { *ok = false; *e = "the link is damaged (bad base64)"; }
            return statusData("off");
        };
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        QSignalSpy spy(&r.ctl, &Controller::importFinished);
        r.ctl.importLink("garbage");
        QTRY_COMPARE(spy.count(), 1);
        QCOMPARE(spy.at(0).at(0).toBool(), false);
        QCOMPARE(spy.at(0).at(1).toString(), QString("the link is damaged (bad base64)"));
    }
    void importLinkWithoutLinkTextIsRefusedLocally() {
        Rig r(sock());
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        QSignalSpy spy(&r.ctl, &Controller::importFinished);
        r.ctl.importLink("   \n ");
        QTRY_COMPARE(spy.count(), 1);
        QCOMPARE(spy.at(0).at(0).toBool(), false);
        QVERIFY(!r.daemon.received.contains("import"));
    }
    void panicCallsPanic() {
        Rig r(sock());
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("off"));
        r.ctl.panic();
        QTRY_VERIFY(r.daemon.received.contains("panic"));
    }
    void downCallsDown() {
        Rig r(sock());
        r.state = "connected";
        QVERIFY(r.daemon.listen(sock()));
        r.client.start();
        QTRY_COMPARE(r.ctl.state(), QString("connected"));
        r.ctl.down();
        QTRY_VERIFY(r.daemon.received.contains("down"));
    }
};

QTEST_MAIN(TstController)
#include "tst_controller.moc"
