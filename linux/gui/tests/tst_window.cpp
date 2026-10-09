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

    App(const QString &sockPath, bool listen = true) {
        daemon.handler = [this](const QString &cmd, const QJsonObject &, bool *ok, QString *err) -> QJsonValue {
            if (cmd == "status")
                return statusData(state, profile, state == "connected", error, longUri);
            if (cmd == "log")
                return QString("node log line");
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
