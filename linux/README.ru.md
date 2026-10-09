# YggTunnel для Linux

**Русский** · [English](README.md)

Клиент для вашего сервера через сеть [Yggdrasil](https://yggdrasil-network.github.io/): то же Go-ядро и те же
профили `yggtunnel://`, что в Android-приложении. Состояние: **0.1.0, командная строка и демон**; окно на Qt,
kill switch и раздельная маршрутизация — следующие шаги.

```
yggtunnelctl ──unix-сокет (JSON)──▶ yggtunneld (root, systemd)
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

## Установка (из исходников)

```sh
sudo install -m755 yggtunneld yggtunnelctl /usr/bin/
sudo install -m644 packaging/yggtunneld.service /etc/systemd/system/
sudo install -m644 packaging/io.github.xtratter.yggtunnel.policy /usr/share/polkit-1/actions/
sudo groupadd -f yggtunnel && sudo usermod -aG yggtunnel "$USER"   # потом перелогиньтесь
sudo systemctl enable --now yggtunneld
```

## Использование

```sh
yggtunnelctl import profile.txt     # или: yggtunnelctl import -   (вставьте ссылку, Ctrl-D)
yggtunnelctl up
yggtunnelctl status
yggtunnelctl down
yggtunnelctl log
yggtunnelctl panic                  # убрать все маршруты, правила и настройки DNS, добавленные демоном
```

Ссылка профиля содержит приватный ключ, поэтому как аргумент командной строки она не принимается (её показал
бы список процессов). Изменение соединения разрешает polkit (`io.github.xtratter.yggtunnel.connect`); сокет
`/run/yggtunnel.sock` принадлежит группе `yggtunnel`.

## Что демон меняет в системе

Пока соединение включено: интерфейс `yggtun0` (адрес узла Yggdrasil `/7`, `clientIp4/32`, `clientIp6/128`,
MTU 1280), маршруты по умолчанию в таблице 51871, два правила `ip rule` (приоритеты 32763 и 32764), таблица
nftables `inet yggtunnel`, которая помечает собственный трафик демона, чтобы он шёл мимо туннеля, и DNS
`1.1.1.1` / `8.8.8.8` на `yggtun0` через systemd-resolved (`/etc/resolv.conf` не редактируется).

Каждое изменение записывается в `/var/lib/yggtunnel/prev.json` до его выполнения и отменяется в обратном порядке
при `down`, при любой ошибке и при следующем запуске, если демон был убит. Страховка — `ExecStopPost` с
`yggtunneld --recover-only`. Режим `yggtunneld --dry-run --no-polkit` только пишет изменения в журнал.

## Пока нет

Окна, kill switch, раздельной маршрутизации (по приложениям и по подсетям или доменам), настроек автовыбора
пиров, пакета (PKGBUILD). См. `docs/superpowers/specs/2026-10-09-linux-client-design.md`.
