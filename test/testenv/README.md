# Testumgebung für UserCleanup

`testenv.sh` baut auf einem **Wegwerf-Testserver** (Debian oder Ubuntu) eine
Umgebung auf, an der sich UserCleanup ausprobieren lässt: Testnutzer mit Homes,
Apache- und Caddy-Seiten, PHP-FPM-Pools, logrotate, Cron, sudoers und eine
systemd-Unit, dazu bewusst eingebaute Problemfälle.

```bash
sudo ./test/testenv/testenv.sh setup                 # Pakete installieren und alles anlegen
sudo ./test/testenv/testenv.sh setup --no-install    # nur Configs/Nutzer, keine Pakete
sudo ./test/testenv/testenv.sh setup --with-broken-pool   # zusätzlich defekter PHP-Pool
sudo ./test/testenv/testenv.sh setup --caddy-port 8088    # Caddy auf anderem Port (Standard 8080)
sudo ./test/testenv/testenv.sh status                # Übersicht
sudo ./test/testenv/testenv.sh teardown              # alles wieder entfernen
```

Das Skript ist idempotent, `setup` kann also mehrfach laufen. Ist der Caddy-Port
schon von einem anderen Dienst belegt, bricht `setup` vor dem Aufbau ab. Startet
ein Dienst nach dem Aufbau nicht, endet `setup` mit Exit-Code 1.

## Was angelegt wird

Alle Objekte beginnen mit `uct-`, Testdomains enden auf `.uct.test` und werden
per `/etc/hosts` auf 127.0.0.1 gelegt. Apache läuft auf Port 80, Caddy auf
Port 8080 (oder dem mit `--caddy-port` gewählten).

| Fall | Nutzer | Was es testet |
|---|---|---|
| gesund | `uct-alice` | Apache-vhost, PHP-Pool, logrotate, sudoers, Nutzer-Crontab |
| gesund | `uct-bob` | Caddy-Site mit `php_fastcgi`, Reverse-Proxy-Site, `/etc/cron.d`, systemd-Unit mit `User=` |
| kein Home | `uct-carol` | passwd-Eintrag ohne Home-Verzeichnis |
| verwaistes Home | `uct-dave` | `/home/uct-dave` ohne passwd-Eintrag (Dateien gehören UID 4711), deaktivierter vhost in `sites-available` |
| gelöschter Nutzer | `uct-erin` | aktiver Apache-vhost und Caddy-Site auf fehlenden Webroot, logrotate mit `su uct-erin` (logrotate meldet Fehler), optional PHP-Pool |
| nologin | `uct-frank` | Shell `/usr/sbin/nologin`, aber aktive Seite |
| Teilstring | `uct-web`, `uct-webadmin` | Namen und Pfade, die sich als Teilstring überschneiden; `uct-webadmin` nutzt den Pool von `uct-web`, `uct-web` hat Seiten in Apache und Caddy |
| Home im Webroot | `uct-gina` | Home ist `/var/www/uct-gina` (`userdel -r` wäre hier gefährlich) |

Dazu kommen Einträge, die UserCleanup Zeile für Zeile auswertet:

| Wo | Was |
|---|---|
| `/etc/crontab` | Job von `uct-alice` und vom gelöschten `uct-erin` |
| `/etc/aliases` | Alias `uct-alice` und `uct-team` (alice, bob) |
| `/etc/security/limits.conf`, `limits.d/uct-frank.conf` | Limit für bob in der gemeinsamen Datei, eigene Datei für frank |
| `/etc/sudoers.d/uct-team`, `uct-erin` | `User_Alias` mit alice und bob, Regel für den gelöschten erin |
| `/etc/ssh/sshd_config`, `sshd_config.d/uct-frank.conf` | `Match User uct-bob` am Dateiende, `DenyUsers uct-frank` |
| systemd | Timer `uct-bob-app.timer`, root-Dienst `uct-alice-export.service` mit alices Home, Drop-in `logrotate.service.d/uct-alice.conf` |
| systemd user | User-Unit in `/home/uct-web/.config/systemd/user` mit linger, linger-Datei für den gelöschten erin |

In gemeinsam genutzten Dateien stehen die Zeilen zwischen `# BEGIN usercleanup-testenv`
und `# END usercleanup-testenv`; `teardown` entfernt genau diesen Block. Die
sshd-Änderung wird mit `sshd -t` geprüft und bei Fehlern zurückgenommen, sshd wird
nicht neu geladen.

Jedes Home bekommt eine `.ssh/authorized_keys` und eine `.env` mit Dummy-Passwort,
damit sich prüfen lässt, ob Backups solche Dateien mit sicheren Rechten ablegen.

`--with-broken-pool` legt einen PHP-FPM-Pool für den nicht existierenden Nutzer
`uct-erin` an. Danach startet php-fpm **nicht mehr** (alle PHP-Seiten fallen aus).
Genau dieser Fall entsteht, wenn ein Nutzer gelöscht wird und sein Pool liegen bleibt.

## Abbau

Alles, was `setup` anlegt, steht im Manifest unter
`/var/lib/usercleanup-testenv/manifest`. `teardown` entfernt nur diese Einträge,
löscht nur Nutzer mit der GECOS-Kennung `usercleanup-testenv` und stellt
überschriebene Dateien (z. B. `/etc/caddy/Caddyfile`) aus der Sicherung wieder
her. Installierte Pakete bleiben. Schlägt etwas fehl, bleibt das Manifest
erhalten und `teardown` kann erneut laufen.

Getestet auf Ubuntu 24.04 (Container ohne systemd: Aufbau, Wiederholung,
Seitenaufrufe über Apache und Caddy, Abbau ohne Rückstände) und auf Debian 12
arm64 mit systemd (Apache und PHP-FPM 8.2 laufen, alle Apache-Seiten antworten).
