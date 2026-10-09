#include <QtTest>
#include <QDir>
#include <QImage>
#include <QJsonArray>
#include <QJsonObject>
#include <QLocale>
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQuickItem>
#include <QQuickStyle>
#include <QQuickWindow>
#include <QTemporaryDir>
#include <QTranslator>
#include <memory>

#include "controller.h"
#include "daemonclient.h"
#include "fakedaemon.h"

static QJsonObject statusData(const QString &state, bool profile = true, bool node = false, const QString &error = {}, bool longUri = false)
{
    QJsonObject o{{"state", state}};
    o["profile"] = profile ? QJsonObject{{"name", "example"}, {"serverYgg", "200:db8::2"}, {"privateKey", "…abcd"}} : QJsonObject();
    if (!error.isEmpty())
        o["error"] = error;
    if (node) {
        QJsonArray peers;
        peers << QJsonObject{{"uri", "tls://192.0.2.1:1234"}, {"up", true}, {"latencyMs", 42.5}}
              << QJsonObject{{"uri", "tls://192.0.2.2:1234"}, {"up", false}, {"error", "connection refused"}}
              << QJsonObject{{"uri", longUri ? QString("wss://very-long-peer-name-") + QString(300, 'a') + ".example.net:443/path?priority=1" : QString("wss://192.0.2.3:443/path?priority=1")},
                         {"up", true}, {"latencyMs", 80}};
        if (longUri)
            peers[1] = QJsonObject{{"uri", "tls://192.0.2.2:1234"}, {"up", false}, {"error", QString("connection refused: ") + QString(400, 'e')}};
        o["node"] = QJsonObject{{"running", true}, {"address", "200:db8::1"}, {"peers", peers},
                                {"tunnel", QJsonObject{{"handshakeAgo", 12}}}};
    }
    return o;
}

static void collectTexts(QObject *o, QStringList &out)
{
    const QVariant t = o->property("text");
    if (t.typeId() == QMetaType::QString)
        out << t.toString();
    for (QObject *c : o->children())
        collectTexts(c, out);
    if (auto *item = qobject_cast<QQuickItem *>(o))
        for (QQuickItem *c : item->childItems())
            collectTexts(c, out);
}

// Items made by a Repeater or shown in a Popup hang in the visual tree, not under their QObject
// parent, so search both trees.
static void findAll(QObject *o, const QString &name, QList<QObject *> &out, QSet<QObject *> &seen)
{
    if (!o || seen.contains(o))
        return;
    seen.insert(o);
    if (o->objectName() == name)
        out << o;
    for (QObject *c : o->children())
        findAll(c, name, out, seen);
    if (auto *item = qobject_cast<QQuickItem *>(o))
        for (QQuickItem *c : item->childItems())
            findAll(c, name, out, seen);
}

static QList<QObject *> findAll(QObject *root, const QString &name)
{
    QList<QObject *> out;
    QSet<QObject *> seen;
    findAll(root, name, out, seen);
    return out;
}

// A running window on top of a fake daemon.
struct App {
    FakeDaemon daemon;
    std::unique_ptr<DaemonClient> client;
    std::unique_ptr<Controller> ctl;
    QQmlApplicationEngine engine;
    QQuickWindow *win = nullptr;
    QString state = "off";
    bool profile = true;
    QString error;
    bool longUri = false;
    QString importError;
    QJsonObject settings{{"killSwitch", false}, {"allowLan", true}};
    bool ksActive = false;
    QJsonObject splitStatus;
    QString setError;

    App(const QString &sockPath, bool listen = true) {
        daemon.handler = [this](const QString &cmd, const QJsonObject &args, bool *ok, QString *err) -> QJsonValue {
            if (cmd == "status") {
                QJsonObject o = statusData(state, profile, state == "connected", error, longUri);
                o["settings"] = settings;
                o["killSwitchActive"] = ksActive;
                if (!splitStatus.isEmpty())
                    o["splitStatus"] = splitStatus;
                return o;
            }
            if (cmd == "log")
                return QString("node log line");
            if (cmd == "set" && !setError.isEmpty()) {
                *ok = false;
                *err = setError;
            } else if (cmd == "set" && args.contains("split")) {
                settings["split"] = args["split"]; // what the daemon would store (already canonical here)
            }
            if (cmd == "import" && !importError.isEmpty()) {
                *ok = false;
                *err = importError;
            }
            return QJsonValue();
        };
        if (listen)
            daemon.listen(sockPath);
        client = std::make_unique<DaemonClient>(sockPath);
        client->setRetryInterval(100);
        ctl = std::make_unique<Controller>(client.get());
        ctl->setPollInterval(100);
        engine.rootContext()->setContextProperty("ctl", ctl.get());
        engine.load(QUrl::fromLocalFile(QString(YGG_QML_DIR) + "/Main.qml"));
        win = qobject_cast<QQuickWindow *>(engine.rootObjects().value(0));
        client->start();
    }
    QObject *item(const char *name) const { return win ? findAll(win, name).value(0) : nullptr; }
    QList<QObject *> items(const char *name) const { return win ? findAll(win, name) : QList<QObject *>(); }
    QString text(const char *name) const { QObject *o = item(name); return o ? o->property("text").toString() : QString(); }
    bool visible(const char *name) const { QObject *o = item(name); return o && o->property("visible").toBool(); }
    void shot(const QString &file) {
        QDir().mkpath(SHOTS_DIR);
        QTest::qWait(1200); // let the animations settle
        const QImage img = win->grabWindow();
        QVERIFY2(!img.isNull(), "grabWindow returned an empty image");
        QVERIFY(img.save(QString(SHOTS_DIR) + "/" + file));
        // not blank: more than one colour in the image
        bool varied = false;
        for (int y = 0; y < img.height() && !varied; y += 7)
            for (int x = 0; x < img.width(); x += 7)
                if (img.pixel(x, y) != img.pixel(0, 0)) { varied = true; break; }
        QVERIFY2(varied, "the window grab is a single colour");
    }
};

class TstWindow : public QObject {
    Q_OBJECT
    QTemporaryDir dir;
    QString sock() const { return dir.filePath("d.sock"); }

private slots:
    void initTestCase() {
        QLocale::setDefault(QLocale(QLocale::English));
        QQuickStyle::setStyle("Material");
    }
    void windowLoads() {
        App a(sock());
        QVERIFY2(a.win, "Main.qml did not load");
        QCOMPARE(a.win->title(), QString("YggTunnel"));
    }
    void showsConnectedState() {
        App a(sock());
        a.state = "connected";
        QTRY_COMPARE(a.win->property("statusText").toString(), QString("Connected"));
        QTRY_COMPARE(a.text("statusLabel"), QString("Connected"));
        QTRY_COMPARE(a.text("primaryButton"), QString("Disconnect"));
        QVERIFY(a.text("serverLabel").contains("200:db8::2"));
    }
    void showsOffStateWithConnectButton() {
        App a(sock());
        QTRY_COMPARE(a.text("statusLabel"), QString("Off"));
        QCOMPARE(a.text("primaryButton"), QString("Connect"));
        QVERIFY(a.item("primaryButton")->property("enabled").toBool());
    }
    void buttonDisabledWhileBusy() {
        App a(sock());
        QTRY_COMPARE(a.text("statusLabel"), QString("Off"));
        QTRY_COMPARE(a.daemon.connections(), 1);
        a.daemon.silent = true; // `up` is never answered: the call stays in flight
        QMetaObject::invokeMethod(a.ctl.get(), "up");
        QTRY_VERIFY(!a.item("primaryButton")->property("enabled").toBool());
    }
    void unreachableShowsHint() {
        App a(sock(), false);
        QTRY_COMPARE(a.text("statusLabel"), QString("Daemon not reachable"));
        QTRY_VERIFY(a.visible("hintLabel"));
        QVERIFY2(a.text("hintLabel").contains("systemctl start yggtunneld"), qPrintable(a.text("hintLabel")));
        QVERIFY2(a.text("hintLabel").contains(sock()), qPrintable(a.text("hintLabel")));
        QVERIFY(!a.visible("primaryButton"));
        QVERIFY(!a.visible("panicButton"));
    }
    void noProfileShowsImportButton() {
        App a(sock());
        a.profile = false;
        QTRY_COMPARE(a.text("statusLabel"), QString("Off"));
        QTRY_COMPARE(a.text("primaryButton"), QString("Import a profile"));
    }
    void islandAppearsOnStateEvent() {
        App a(sock());
        QTRY_COMPARE(a.text("statusLabel"), QString("Off"));
        QTRY_COMPARE(a.daemon.connections(), 1);
        a.ctl->setPollInterval(60000);
        a.daemon.pushEvent("state", QJsonObject{{"state", "starting"}});
        QTRY_VERIFY(a.item("island")->property("shown").toBool());
        QVERIFY2(a.text("islandLabel").contains("Connecting"), qPrintable(a.text("islandLabel")));
    }
    void islandSaysDisconnectedAfterConnection() {
        App a(sock());
        a.state = "connected";
        QTRY_COMPARE(a.text("statusLabel"), QString("Connected"));
        a.state = "off";
        a.daemon.pushEvent("state", QJsonObject{{"state", "off"}});
        QTRY_VERIFY(a.text("islandLabel").contains("Disconnected"));
    }
    void islandConnectedDoesNotClaimZeroPeers() {
        App a(sock());
        QTRY_COMPARE(a.text("statusLabel"), QString("Off"));
        QTRY_COMPARE(a.daemon.connections(), 1);
        a.ctl->setPollInterval(60000); // the poll is behind `up`: only the event has arrived
        a.daemon.pushEvent("state", QJsonObject{{"state", "connected"}});
        QTRY_VERIFY(a.item("island")->property("shown").toBool());
        QCOMPARE(a.text("islandLabel"), QString("Connected"));
        a.state = "connected"; // the next status reply brings the peers; the island follows
        a.ctl->setPollInterval(50);
        QTRY_COMPARE(a.text("islandLabel"), QString("Connected · 2 peers"));
    }
    void stickyIslandStaysWhileConnectingAndHidesWhenDaemonGoesAway() {
        App a(sock());
        QVERIFY2(a.item("island")->metaObject()->indexOfProperty("hideDelay") >= 0, "Island has no hideDelay property");
        a.item("island")->setProperty("hideDelay", 300);
        QTRY_COMPARE(a.text("statusLabel"), QString("Off"));
        QTRY_COMPARE(a.daemon.connections(), 1);
        a.ctl->setPollInterval(60000);
        a.daemon.pushEvent("state", QJsonObject{{"state", "starting"}});
        QTRY_VERIFY(a.item("island")->property("shown").toBool());
        QTest::qWait(900); // three times the hide delay
        QVERIFY2(a.item("island")->property("shown").toBool(), "a sticky island must stay while connecting");
        a.daemon.close();
        QTRY_VERIFY(!a.item("island")->property("shown").toBool());
    }
    void protectionCardShowsStates() {
        App a(sock());
        QTRY_VERIFY(a.visible("protectionCard"));
        QTRY_COMPARE(a.text("protectionStatus"), QString("Off"));
        a.settings = QJsonObject{{"killSwitch", true}, {"allowLan", true}};
        QTRY_COMPARE(a.text("protectionStatus"), QString("Armed: it starts with the next connection"));
        a.state = "connected";
        a.ksActive = true;
        QTRY_COMPARE(a.text("protectionStatus"), QString("Active"));
        QVERIFY(a.item("killSwitchSwitch")->property("checked").toBool());
    }
    void lanSwitchDisabledWhileKillSwitchOff() {
        App a(sock());
        QTRY_VERIFY(a.visible("protectionCard"));
        QVERIFY(!a.item("lanSwitch")->property("enabled").toBool());
        a.settings = QJsonObject{{"killSwitch", true}, {"allowLan", true}};
        QTRY_VERIFY(a.item("lanSwitch")->property("enabled").toBool());
    }
    void togglingSendsSet() {
        App a(sock());
        QTRY_VERIFY(a.visible("protectionCard"));
        QTRY_COMPARE(a.daemon.connections(), 1);
        a.item("killSwitchSwitch")->setProperty("checked", true); // what a click does ...
        QMetaObject::invokeMethod(a.item("killSwitchSwitch"), "clicked"); // ... before it emits clicked
        QTRY_VERIFY(a.daemon.received.contains("set"));
        QCOMPARE(a.daemon.receivedArgs.at(a.daemon.received.indexOf("set")), (QJsonObject{{"killSwitch", true}}));
    }
    static QJsonObject splitSettings(const QString &mode, const QStringList &subnets, const QStringList &domains) {
        QJsonArray s, d;
        for (const QString &x : subnets) s << x;
        for (const QString &x : domains) d << x;
        return QJsonObject{{"killSwitch", false}, {"allowLan", true}, {"split", QJsonObject{{"mode", mode}, {"subnets", s}, {"domains", d}}}};
    }
    void routingCardShowsModesAndFields() {
        App a(sock());
        a.settings = splitSettings("exclude", {"203.0.113.0/24", "198.51.100.0/24"}, {"example.com"});
        QTRY_VERIFY(a.visible("routingCard"));
        QTRY_VERIFY(a.item("modeExclude")->property("checked").toBool());
        QVERIFY(!a.item("modeAll")->property("checked").toBool());
        QTRY_COMPARE(a.text("subnetsField"), QString("203.0.113.0/24\n198.51.100.0/24"));
        QCOMPARE(a.text("domainsField"), QString("example.com"));
    }
    void fieldsHiddenInModeAll() {
        App a(sock());
        QTRY_VERIFY(a.visible("routingCard"));
        QVERIFY(a.item("modeAll")->property("checked").toBool());
        QVERIFY(!a.visible("subnetsField"));
        QVERIFY(!a.visible("domainsField"));
    }
    void modeRadiosDisabledWhileConnected() {
        App a(sock());
        a.state = "connected";
        a.settings = splitSettings("only", {"203.0.113.0/24"}, {});
        QTRY_COMPARE(a.text("statusLabel"), QString("Connected"));
        QTRY_VERIFY(!a.item("modeOnly")->property("enabled").toBool());
        QVERIFY(!a.item("modeAll")->property("enabled").toBool());
        QVERIFY(a.visible("modeLockedHint"));
        QVERIFY(a.item("applyButton")->property("enabled").toBool()); // the lists can still be changed
    }
    void modeRadiosEnabledWhileDisconnected() {
        App a(sock());
        QTRY_VERIFY(a.visible("routingCard"));
        QVERIFY(a.item("modeOnly")->property("enabled").toBool());
        QVERIFY(!a.visible("modeLockedHint"));
    }
    void applySendsSplit() {
        App a(sock());
        QTRY_VERIFY(a.visible("routingCard"));
        QTRY_COMPARE(a.daemon.connections(), 1);
        QMetaObject::invokeMethod(a.item("modeExclude"), "clicked");
        QTRY_VERIFY(a.visible("subnetsField"));
        a.item("subnetsField")->setProperty("text", "203.0.113.0/24\n\n  198.51.100.0/24 \n");
        a.item("domainsField")->setProperty("text", "example.com\r\n");
        QMetaObject::invokeMethod(a.item("applyButton"), "clicked");
        QTRY_VERIFY(a.daemon.received.contains("set"));
        const QJsonObject s = a.daemon.receivedArgs.at(a.daemon.received.indexOf("set"))["split"].toObject();
        QCOMPARE(s["mode"].toString(), QString("exclude"));
        QCOMPARE(s["subnets"].toArray(), (QJsonArray{"203.0.113.0/24", "198.51.100.0/24"}));
        QCOMPARE(s["domains"].toArray(), (QJsonArray{"example.com"}));
    }
    void applyShowsDaemonError() {
        App a(sock());
        a.setError = "the link is not valid: 0.0.0.0/0 is a default route";
        QTRY_VERIFY(a.visible("routingCard"));
        QTRY_COMPARE(a.daemon.connections(), 1);
        QMetaObject::invokeMethod(a.item("modeOnly"), "clicked");
        QMetaObject::invokeMethod(a.item("applyButton"), "clicked");
        QTRY_VERIFY(a.visible("routingError"));
        QVERIFY(a.text("routingError").contains("default route"));
    }
    void failedApplyKeepsTheUsersEdits() {
        App a(sock());
        a.settings = splitSettings("exclude", {"203.0.113.0/24"}, {});
        a.setError = "\"bogus\" is not a valid address or subnet";
        QTRY_COMPARE(a.text("subnetsField"), QString("203.0.113.0/24"));
        QTRY_COMPARE(a.daemon.connections(), 1);
        a.item("subnetsField")->setProperty("text", "198.51.100.0/24\nbogus");
        QMetaObject::invokeMethod(a.item("applyButton"), "clicked");
        QTRY_VERIFY(a.visible("routingError"));
        QVERIFY(a.text("routingError").contains("bogus"));
        QTest::qWait(500); // polls keep coming
        QCOMPARE(a.text("subnetsField"), QString("198.51.100.0/24\nbogus")); // nothing is lost: the user fixes the typo
    }
    void successfulApplyShowsTheStoredForm() {
        App a(sock());
        a.settings = splitSettings("exclude", {"203.0.113.0/24"}, {});
        QTRY_COMPARE(a.text("subnetsField"), QString("203.0.113.0/24"));
        QTRY_COMPARE(a.daemon.connections(), 1);
        a.item("subnetsField")->setProperty("text", "198.51.100.0/24\n\n192.0.2.0/24\n");
        QMetaObject::invokeMethod(a.item("applyButton"), "clicked");
        QTRY_COMPARE(a.text("subnetsField"), QString("198.51.100.0/24\n192.0.2.0/24"));
        QVERIFY(!a.visible("routingError"));
    }
    void unrelatedErrorsDoNotAppearUnderApply() {
        App a(sock());
        QTRY_VERIFY(a.visible("routingCard"));
        QTRY_COMPARE(a.daemon.connections(), 1);
        QMetaObject::invokeMethod(a.item("modeExclude"), "clicked");
        QMetaObject::invokeMethod(a.item("applyButton"), "clicked");
        QTest::qWait(300);
        a.error = "step failed"; // a later, unrelated connection error
        QTRY_VERIFY(a.visible("errorLabel"));
        QVERIFY(!a.visible("routingError"));
    }
    void protectionNoteFollowsTheRoutingMode() {
        App a(sock());
        QTRY_VERIFY(a.visible("protectionCard"));
        QVERIFY(a.text("protectionNote").contains("not going through the tunnel"));
        a.settings = splitSettings("only", {"203.0.113.0/24"}, {});
        QTRY_VERIFY(a.text("protectionNote").contains("meant for the tunnel"));
    }
    void editedTextSurvivesStatusPolls() {
        App a(sock());
        a.settings = splitSettings("exclude", {"203.0.113.0/24"}, {});
        QTRY_COMPARE(a.text("subnetsField"), QString("203.0.113.0/24"));
        a.item("subnetsField")->setProperty("text", "198.51.100.0/24\n192.0.2.0/24");
        QTest::qWait(500); // several polls with unchanged server data
        QCOMPARE(a.text("subnetsField"), QString("198.51.100.0/24\n192.0.2.0/24"));
        a.settings = splitSettings("exclude", {"10.1.0.0/16"}, {}); // the server's data changed (another client)
        QTRY_COMPARE(a.text("subnetsField"), QString("10.1.0.0/16"));
    }
    void resolverStatusIsShown() {
        App a(sock());
        a.state = "connected";
        a.settings = splitSettings("only", {}, {"example.com"});
        a.splitStatus = QJsonObject{{"mode", "only"}, {"resolved", 5}, {"resolveError", "example.org: servfail"}};
        QTRY_VERIFY(a.visible("resolvedLabel"));
        QVERIFY(a.text("resolvedLabel").contains("5"));
        QTRY_VERIFY(a.visible("resolveErrorLabel"));
        QVERIFY(a.text("resolveErrorLabel").contains("servfail"));
    }
    void longListDoesNotBreakLayout() {
        App a(sock());
        QStringList many;
        for (int i = 0; i < 300; ++i)
            many << QString("10.%1.%2.0/24").arg(i / 256).arg(i % 256);
        a.settings = splitSettings("exclude", many, {"a-very-long-name-" + QString(200, 'x') + ".example.com"});
        QTRY_COMPARE(a.item("subnetsField")->property("text").toString().count('\n'), 299);
        QTest::qWait(300);
        auto *card = qobject_cast<QQuickItem *>(a.item("routingCard"));
        QVERIFY(card->width() <= a.win->width());
        auto *apply = qobject_cast<QQuickItem *>(a.item("applyButton"));
        const QPointF right = apply->mapToItem(card, QPointF(apply->width(), 0));
        QVERIFY2(right.x() <= card->width() + 0.5, "the Apply button sticks out of the card");
    }
    void routingCardHiddenWhenUnreachable() {
        App a(sock(), false);
        QTRY_COMPARE(a.text("statusLabel"), QString("Daemon not reachable"));
        QVERIFY(!a.visible("routingCard"));
    }
    void grabsRoutingScreenshot() {
        App a(sock());
        a.state = "connected";
        a.settings = splitSettings("only", {"203.0.113.0/24", "2001:db8:77::/48"}, {"example.com", "example.net"});
        a.splitStatus = QJsonObject{{"mode", "only"}, {"resolved", 5}, {"resolveError", "example.org: servfail"}};
        QTRY_VERIFY(a.visible("resolvedLabel"));
        a.win->setHeight(1500); // the card is below the fold of the normal window size
        QTest::qWait(300);
        a.shot("routing.png");
    }
    void protectionCardHiddenWhenUnreachable() {
        App a(sock(), false);
        QTRY_COMPARE(a.text("statusLabel"), QString("Daemon not reachable"));
        QVERIFY(!a.visible("protectionCard"));
    }
    void grabsProtectionScreenshot() {
        App a(sock());
        a.state = "connected";
        a.settings = QJsonObject{{"killSwitch", true}, {"allowLan", false}};
        a.ksActive = true;
        QTRY_COMPARE(a.text("protectionStatus"), QString("Active"));
        a.shot("protection.png");
    }
    void islandStaysQuietAtStartup() {
        App a(sock());
        QTRY_COMPARE(a.text("statusLabel"), QString("Off"));
        QTest::qWait(200);
        QVERIFY(!a.item("island")->property("shown").toBool());
    }
    void errorTextIsShown() {
        App a(sock());
        a.state = "off";
        a.error = "step failed";
        QTRY_VERIFY(a.visible("errorLabel"));
        QVERIFY(a.text("errorLabel").contains("step failed"));
    }
    void peersCardListsPeers() {
        App a(sock());
        a.state = "connected";
        QTRY_COMPARE(a.text("statusLabel"), QString("Connected"));
        QTRY_VERIFY(a.visible("peersCard"));
        auto uris = a.items("peerUri");
        QTRY_COMPARE((uris = a.items("peerUri")).size(), 3);
        QCOMPARE(uris[0]->property("text").toString(), QString("tls://192.0.2.1:1234"));
        QVERIFY(a.text("peerLatency").contains("43")); // 42.5 ms, rounded
        const auto errs = a.items("peerError");
        QStringList shown; // every peer has an error label; only the failed one is visible
        for (QObject *e : errs)
            if (e->property("visible").toBool())
                shown << e->property("text").toString();
        QCOMPARE(shown, QStringList{"connection refused"});
        QVERIFY(!a.visible("peersEmpty"));
    }
    void peersCardHiddenWhenOff() {
        App a(sock());
        QTRY_COMPARE(a.text("statusLabel"), QString("Off"));
        QVERIFY(!a.visible("peersCard"));
    }
    void longUriIsElided() {
        App a(sock());
        a.state = "connected";
        a.longUri = true;
        QTRY_COMPARE(a.text("statusLabel"), QString("Connected"));
        QTRY_VERIFY(a.item("peersCard") && !a.items("peerUri").isEmpty());
        auto *card = qobject_cast<QQuickItem *>(a.item("peersCard"));
        for (QObject *o : a.items("peerUri")) {
            auto *label = qobject_cast<QQuickItem *>(o);
            const QPointF p = label->mapToItem(card, QPointF(label->width(), 0));
            QVERIFY2(p.x() <= card->width() + 0.5, qPrintable(QString("label right edge %1 > card width %2").arg(p.x()).arg(card->width())));
        }
        for (QObject *o : a.items("peerError")) {
            auto *label = qobject_cast<QQuickItem *>(o);
            QVERIFY(label->mapToItem(card, QPointF(label->width(), 0)).x() <= card->width() + 0.5);
        }
    }
    void importDialogShowsDaemonError() {
        App a(sock());
        a.importError = "the link is damaged (bad base64)";
        QTRY_COMPARE(a.text("statusLabel"), QString("Off"));
        QMetaObject::invokeMethod(a.item("importDialog"), "open");
        QTRY_VERIFY(a.item("linkField"));
        a.item("linkField")->setProperty("text", "garbage");
        QMetaObject::invokeMethod(a.item("importDialog"), "submit");
        QTRY_VERIFY(a.visible("importError"));
        QCOMPARE(a.text("importError"), QString("the link is damaged (bad base64)"));
        QVERIFY(a.item("importDialog")->property("visible").toBool()); // stays open
        QTest::qWait(600); // the opening animation ends meanwhile and must not clear the message
        QVERIFY(a.visible("importError"));
    }
    void importDialogClosesOnSuccess() {
        App a(sock());
        QTRY_COMPARE(a.text("statusLabel"), QString("Off"));
        QMetaObject::invokeMethod(a.item("importDialog"), "open");
        QTRY_VERIFY(a.item("linkField"));
        a.item("linkField")->setProperty("text", "yggtunnel://import#abc");
        QMetaObject::invokeMethod(a.item("importDialog"), "submit");
        QTRY_VERIFY(!a.item("importDialog")->property("visible").toBool());
        QVERIFY(a.daemon.received.contains("import"));
    }
    void importButtonOpensDialogWhenNoProfile() {
        App a(sock());
        a.profile = false;
        QTRY_COMPARE(a.text("primaryButton"), QString("Import a profile"));
        QMetaObject::invokeMethod(a.item("primaryButton"), "clicked");
        QTRY_VERIFY(a.item("importDialog")->property("visible").toBool());
    }
    void logTabsShowHistoryAndNodeLog() {
        App a(sock());
        a.state = "connected";
        QTRY_COMPARE(a.text("statusLabel"), QString("Connected"));
        QMetaObject::invokeMethod(a.item("logButton"), "clicked");
        QTRY_VERIFY(a.visible("logPage"));
        QTRY_VERIFY(a.text("historyText").contains("connected"));
        a.item("logTabs")->setProperty("currentIndex", 1);
        QTRY_COMPARE(a.text("nodeLogText"), QString("node log line"));
    }
    void noPrivateKeyInUi() {
        App a(sock());
        a.state = "connected";
        QTRY_COMPARE(a.text("statusLabel"), QString("Connected"));
        QMetaObject::invokeMethod(a.item("importDialog"), "open");
        QMetaObject::invokeMethod(a.item("logButton"), "clicked");
        QTRY_VERIFY(a.visible("logPage"));
        QTest::qWait(300);
        QStringList texts;
        collectTexts(a.win, texts);
        QVERIFY2(texts.size() > 10, "the walk found almost no text; the helper is broken");
        QVERIFY2(!texts.join('\n').contains("abcd"), "the private key tail is visible somewhere in the UI");
    }
    void grabsPeersAndLogScreenshots() {
        App a(sock());
        a.state = "connected";
        a.longUri = true;
        QTRY_VERIFY(a.items("peerUri").size() == 3);
        a.shot("peers.png");
        QMetaObject::invokeMethod(a.item("logButton"), "clicked");
        QTRY_VERIFY(a.visible("logPage"));
        a.item("logTabs")->setProperty("currentIndex", 1);
        QTRY_COMPARE(a.text("nodeLogText"), QString("node log line"));
        a.shot("log.png");
    }
    void russianTranslationIsUsed() {
        QTranslator tr;
        QVERIFY2(tr.load(QString(YGG_QM_FILE)), "yggtunnel_ru.qm is not built");
        QVERIFY(QCoreApplication::installTranslator(&tr));
        {
            App a(sock());
            a.state = "connected";
            QTRY_COMPARE(a.text("statusLabel"), QString("Подключено"));
            QCOMPARE(a.text("primaryButton"), QString("Отключить"));
            QCOMPARE(a.text("panicButton"), QString("Аварийно отключить всё"));
            QTRY_VERIFY(a.visible("peersCard"));
            a.shot("connected-ru.png");
        }
        {
            App a(dir.filePath("none-ru.sock"), false);
            QTRY_COMPARE(a.text("statusLabel"), QString("Демон недоступен"));
            QTRY_VERIFY(a.text("hintLabel").contains("сокет не найден"));
        }
        QCoreApplication::removeTranslator(&tr);
    }
    void grabsImportDialogScreenshot() {
        App a(sock());
        a.importError = "the link is damaged (bad base64)";
        QTRY_COMPARE(a.text("statusLabel"), QString("Off"));
        QMetaObject::invokeMethod(a.item("importDialog"), "open");
        QTRY_VERIFY(a.item("linkField"));
        a.item("linkField")->setProperty("text", "yggtunnel://import#not-a-real-link");
        QMetaObject::invokeMethod(a.item("importDialog"), "submit");
        QTRY_VERIFY(a.visible("importError"));
        a.shot("import.png");
    }
    void grabsScreenshots() {
        {
            App a(sock());
            a.state = "connected";
            QTRY_COMPARE(a.text("statusLabel"), QString("Connected"));
            a.shot("connected.png");
        }
        {
            App a(dir.filePath("none.sock"), false);
            QTRY_COMPARE(a.text("statusLabel"), QString("Daemon not reachable"));
            a.shot("unreachable.png");
        }
        {
            App a(dir.filePath("np.sock"));
            a.profile = false;
            QTRY_COMPARE(a.text("primaryButton"), QString("Import a profile"));
            a.shot("noprofile.png");
        }
    }
};

QTEST_MAIN(TstWindow)
#include "tst_window.moc"
