<img src="fastlane/metadata/android/en-US/images/icon.png" width="96" align="right">

# 🌳 YggTunnel

[![Build](https://github.com/Xtratter/yggtunnel/actions/workflows/build.yml/badge.svg)](https://github.com/Xtratter/yggtunnel/actions/workflows/build.yml)

**Русский** · [English](README.md)

Android-приложение, которое подключается **к вашему серверу через сеть [Yggdrasil](https://yggdrasil-network.github.io/)**
и по шагам превращается в полноценный VPN — по образцу AmneziaVPN, но с Yggdrasil в роли транспорта.
Телефон не соединяется с сервером напрямую: он входит в Yggdrasil через публичные пиры (TLS, QUIC, WebSocket),
поэтому туннель не зависит от прямой связи с адресом сервера.

> [!NOTE]
> Ранняя стадия. Версия 0.4: настройка сервера по SSH из приложения, полный туннель через него, автовыбор пиров, приложения через VPN или мимо (белый и чёрный список), управление устройствами с QR-профилями.

## Скриншоты

| Подключено через сервер | Сервер и обёртка | Пиры |
|:---:|:---:|:---:|
| <img src="fastlane/metadata/android/en-US/images/phoneScreenshots/1.jpg" width="240"> | <img src="fastlane/metadata/android/en-US/images/phoneScreenshots/2.jpg" width="240"> | <img src="fastlane/metadata/android/en-US/images/phoneScreenshots/3.jpg" width="240"> |
| **Устройства** | **Приложения и VPN** | **Подсказки по долгому нажатию** |
| <img src="fastlane/metadata/android/en-US/images/phoneScreenshots/4.jpg" width="240"> | <img src="fastlane/metadata/android/en-US/images/phoneScreenshots/5.jpg" width="240"> | <img src="fastlane/metadata/android/en-US/images/phoneScreenshots/6.jpg" width="240"> |

## Как устроено

```
приложения → TUN → wireguard-go ──UDP/IPv6──▶ yggdrasil-go ──TLS/QUIC/WSS──▶ публичные пиры ──▶ ваш сервер ──▶ интернет
             (0.0.0.0/0, ::/0)        (в том же процессе)                              (yggdrasil + WireGuard + NAT)
```

- Нативное ядро (`go/`) на Go: [yggdrasil-go](https://github.com/yggdrasil-network/yggdrasil-go) и небольшая
  JNI-прослойка, написанная вручную, собирается в `libygg.so` через `go build -buildmode=c-shared` — без gomobile,
  поэтому собирается прямо в Termux его clang, а на обычной машине — Android NDK.
- Приложение (`app/`) на Kotlin без библиотек: `VpnService` и один экран на
  [android-ui-kit](https://github.com/Xtratter/android-ui-kit) (Material 3 Expressive).

## План

| Версия | Что |
|---|---|
| 0.1 | Клиент Yggdrasil: подключение, адрес, пиры с задержкой и трафиком, журнал |
| 0.2 | Настройка сервера по SSH из приложения (ключ, свой порт), как в AmneziaVPN: Yggdrasil, WireGuard, NAT |
| 0.3 | Полный туннель: WireGuard внутри Yggdrasil до вашего сервера |
| 0.4 | Менеджер пиров с автовыбором, QR-профили, туннель для выбранных приложений |
| 0.5 | Управление устройствами (имена, в сети, трафик, удаление), белый и чёрный список приложений |
| 0.6 | Постоянный VPN, плитка в шторке, пиры из публичного каталога (телефон и сервер) |
| 0.7 | Обёртка пиринга: wss через ваш HTTPS-сайт на сервере |
| **0.8** | Обёртка настраивается сама: существующий сайт в nginx (в том числе за telemt / xray на 443) или новый сайт с Let's Encrypt |

## Скачать

[Релизы](https://github.com/Xtratter/yggtunnel/releases): `YggTunnel-vX.Y.apk` — телефоны arm64 (большинство), `YggTunnel-vX.Y-universal.apk` — любое устройство (arm64, 32-битный ARM, x86_64).

## Сборка

В Termux (см. [termux-android-build](https://github.com/Xtratter/termux-android-build)) дополнительно нужен
`pkg install golang`; на других системах — Go 1.25+ и Android NDK (`ANDROID_NDK_HOME`). Затем:

```sh
./gradlew assembleRelease   # сначала запускает go/build.sh (задача Gradle buildGo)
```

**Исправленная зависимость:** ironwood (ядро маршрутизации Yggdrasil) — исправленная копия в
`go/third_party/ironwood`: добавлен только счётчик ожидающих отправки байт для параллельных каналов;
сам yggdrasil-go не изменён. Перед обновлением yggdrasil-go или ironwood прочтите
[go/third_party/PATCHES.ru.md](go/third_party/PATCHES.ru.md).

## Лицензия

GPL-3.0. Yggdrasil — LGPL-3.0.
