# YggTunnel для Linux

**Русский** · [English](README.md)

Клиент для вашего сервера через сеть [Yggdrasil](https://yggdrasil-network.github.io/): то же Go-ядро и те же
профили `yggtunnel://`, что в Android-приложении. Состояние: **0.3.0, демон, командная строка, окно на Qt и kill switch**;
раздельная маршрутизация — следующий шаг.

```
yggtunnel-gui ──┐
                ├─ unix-сокет (JSON) ─▶ yggtunneld (root, systemd)
yggtunnelctl ───┘
                                        ├─ core     Yggdrasil + WireGuard (общее с Android, go/core)
                                        ├─ netconf  TUN, адреса, маршруты, ip rule, метка трафика
                                        ├─ dns      systemd-resolved, только на интерфейсе туннеля
                                        └─ store    профиль (приватный ключ зашифрован), запись для отката
```

## Сборка

Нужен Go (версия из `go/go.mod`; `GOTOOLCHAIN=auto` скачает нужную), JDK и NDK не нужны.

```sh
cd linux
go build -o yggtunneld ./daemon
go build -o yggtunnelctl ./cli
go vet ./... && go test ./...
```

Сетевые тесты идут в одноразовом пространстве имён (`unshare -rn`) и не трогают сеть компьютера; там, где
пространства имён недоступны, они пропускаются с сообщением.

### Окно

Команды выполняются из каталога `linux/`. Нужны Qt 6 (`qt6-declarative`, `qt6-tools`), CMake и Ninja.

```sh
cmake -S gui -B gui/build -G Ninja && cmake --build gui/build
ctest --test-dir gui/build        # без экрана; скриншоты пишутся в gui/build/shots
gui/build/yggtunnel-gui
```

Окно показывает состояние, сервер и пиры, подключает и отключает, импортирует профиль (вставьте ссылку или
откройте файл), показывает историю соединения и журнал узла, а кнопка «Аварийно отключить всё» делает
`panic`. Оно общается только с демоном, поэтому ваш пользователь должен входить в группу `yggtunnel`.
Английский и русский выбираются по языку системы; `YGGTUNNEL_SOCKET` задаёт другой путь к сокету.

## Установка (из исходников)

```sh
sudo install -m755 yggtunneld yggtunnelctl /usr/bin/
sudo install -m644 packaging/yggtunneld.service /etc/systemd/system/
sudo install -m644 packaging/io.github.xtratter.yggtunnel.policy /usr/share/polkit-1/actions/
sudo groupadd -f yggtunnel && sudo usermod -aG yggtunnel "$USER"   # потом перелогиньтесь
sudo systemctl enable --now yggtunneld
sudo cmake --install gui/build --prefix /usr   # окно, пункт меню и значок
```

## Использование

```sh
yggtunnelctl import profile.txt     # или: yggtunnelctl import -   (вставьте ссылку, Ctrl-D)
yggtunnelctl up
yggtunnelctl status
yggtunnelctl down
yggtunnelctl log
yggtunnelctl killswitch on          # отбрасывать трафик мимо туннеля, пока подключены (по умолчанию выключено)
yggtunnelctl lan off                # с kill switch: отбрасывать и трафик локальной сети (по умолчанию разрешён)
yggtunnelctl panic                  # убрать все маршруты, правила и настройки DNS, добавленные демоном
```

Ссылка профиля содержит приватный ключ, поэтому как аргумент командной строки она не принимается (её показал
бы список процессов). Изменение соединения разрешает polkit (`io.github.xtratter.yggtunnel.connect`); сокет
`/run/yggtunnel.sock` принадлежит группе `yggtunnel`.

## Kill switch

Когда включено `killswitch on` и туннель поднят, каждый пакет, который эта машина отправляет **не** через
`yggtun0`, отбрасывается: потерянный маршрут туннеля не может «протечь» в физическую сеть. Всегда пропускаются:
loopback, сам туннель, собственный трафик демона (пиры и сервер; он узнаётся по cgroup), DHCP, обнаружение
соседей IPv6 и, если не выполнено `lan off`, адреса локальной сети (`10/8`, `172.16/12`, `192.168/16`,
`169.254/16`, multicast, широковещательный адрес, `fe80::/10`, `fc00::/7`).

Он включается после подключения туннеля и снимается командой `down`, неудачным `up`, `panic` и остановкой
демона. В окне те же два переключателя в карточке «Защита». Проверить вручную:
`sudo nft list table inet yggtunnelks` (счётчик на правиле `drop` показывает, что было остановлено).

Ограничения сделаны намеренно: если демон убит или упал, kill switch снимается (`ExecStopPost`), потому что
сбой не должен оставлять вас без сети; трафик, пересылаемый через эту машину (контейнеры, мосты виртуальных
машин), не охватывается; до завершения `up` ничего не отбрасывается.

## Что демон меняет в системе

Пока соединение включено: интерфейс `yggtun0` (адрес узла Yggdrasil `/7`, `clientIp4/32`, `clientIp6/128`,
MTU 1280), маршруты по умолчанию в таблице 51871, два правила `ip rule` (приоритеты 32763 и 32764), таблица
nftables `inet yggtunnel`, которая помечает собственный трафик демона, чтобы он шёл мимо туннеля (с подменой адреса на адрес исходящего интерфейса), и DNS
`1.1.1.1` / `8.8.8.8` на `yggtun0` через systemd-resolved (`/etc/resolv.conf` не редактируется). С kill switch добавляется таблица nftables
`inet yggtunnelks`.

Каждое изменение записывается в `/var/lib/yggtunnel/prev.json` до его выполнения и отменяется в обратном порядке
при `down`, при любой ошибке и при следующем запуске, если демон был убит. Страховка — `ExecStopPost` с
`yggtunneld --recover-only`. Режим `yggtunneld --dry-run --no-polkit` только пишет изменения в журнал.

## Пока нет

Отсутствуют: раздельная маршрутизация (по приложениям и по подсетям или доменам), настройки автовыбора пиров и каналов в окне, значок в трее, пакет (PKGBUILD). См. `docs/superpowers/specs/2026-10-09-linux-client-design.md`.
