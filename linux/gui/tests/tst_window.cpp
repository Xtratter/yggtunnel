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
#include <memory>

#include "controller.h"
#include "daemonclient.h"
#include "fakedaemon.h"

static QJsonObject statusData(const QString &state, bool profile = true, bool node = false, const QString &error = {})
{
    QJsonObject o{{"state", state}};
    o["profile"] = profile ? QJsonObject{{"name", "example"}, {"serverYgg", "200:db8::2"}, {"privateKey", "…abcd"}} : QJsonObject();
    if (!error.isEmpty())
        o["error"] = error;
    if (node) {
        QJsonArray peers;
        peers << QJsonObject{{"uri", "tls://192.0.2.1:1234"}, {"up", true}, {"latencyMs", 42.5}}
              << QJsonObject{{"uri", "tls://192.0.2.2:1234"}, {"up", false}, {"error", "connection refused"}}
              << QJsonObject{{"uri", "wss://192.0.2.3:443/path?priority=1"}, {"up", true}, {"latencyMs", 80}};
        o["node"] = QJsonObject{{"running", true}, {"address", "200:db8::1"}, {"peers", peers},
                                {"tunnel", QJsonObject{{"handshakeAgo", 12}}}};
    }
    return o;
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

    App(const QString &sockPath, bool listen = true) {
        daemon.handler = [this](const QString &cmd, const QJsonObject &, bool *, QString *) -> QJsonValue {
            if (cmd == "status")
                return statusData(state, profile, state == "connected", error);
            if (cmd == "log")
                return QString("node log line");
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
    QObject *item(const char *name) const { return win ? win->findChild<QObject *>(name) : nullptr; }
    QString text(const char *name) const { QObject *o = item(name); return o ? o->property("text").toString() : QString(); }
    bool visible(const char *name) const { QObject *o = item(name); return o && o->property("visible").toBool(); }
    void shot(const QString &file) {
        QDir().mkpath(SHOTS_DIR);
        QTest::qWait(400); // let the animations settle
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
