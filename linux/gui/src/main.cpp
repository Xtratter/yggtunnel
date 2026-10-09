#include <QGuiApplication>
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQuickStyle>
#include <QtGlobal>

#include "controller.h"
#include "daemonclient.h"

int main(int argc, char *argv[])
{
    QGuiApplication app(argc, argv);
    app.setApplicationName("yggtunnel-gui");
    app.setApplicationVersion(YGG_VERSION);
    app.setDesktopFileName("yggtunnel-gui");
    QQuickStyle::setStyle("Material");

    const QString socket = qEnvironmentVariableIsSet("YGGTUNNEL_SOCKET")
        ? qEnvironmentVariable("YGGTUNNEL_SOCKET")
        : QStringLiteral("/run/yggtunnel.sock");
    DaemonClient client(socket);
    Controller controller(&client);

    QQmlApplicationEngine engine;
    engine.rootContext()->setContextProperty("ctl", &controller);
    QObject::connect(&engine, &QQmlApplicationEngine::objectCreationFailed, &app, [] { QCoreApplication::exit(1); },
                     Qt::QueuedConnection);
    engine.load(QUrl("qrc:/qml/Main.qml"));
    client.start();
    return app.exec();
}
