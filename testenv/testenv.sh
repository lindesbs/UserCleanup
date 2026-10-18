#!/usr/bin/env bash
#
# testenv.sh - baut auf einem Debian/Ubuntu-Testserver eine Umgebung fuer
# UserCleanup auf (Nutzer, Homes, Apache- und Caddy-vhosts, PHP-FPM-Pools,
# logrotate, Cron, sudoers, systemd) inklusive bewusster Problemfaelle.
#
# NUR AUF WEGWERF-TESTSERVERN AUSFUEHREN.
#
#   sudo ./testenv.sh setup     [--no-install] [--with-broken-pool]
#   sudo ./testenv.sh status
#   sudo ./testenv.sh teardown
#
# Alles, was angelegt wird, steht im Manifest unter $STATE_DIR. "teardown"
# entfernt ausschliesslich diese Eintraege und stellt gesicherte Originale
# wieder her. Installierte Pakete bleiben bestehen.

set -euo pipefail

STATE_DIR=/var/lib/usercleanup-testenv
MANIFEST=$STATE_DIR/manifest
ORIG_DIR=$STATE_DIR/orig
MARKER="usercleanup-testenv"   # GECOS-Kennung der Testnutzer
PREFIX="uct-"                  # Namenspraefix aller Testobjekte
DOMAIN="uct.test"
ORPHAN_UID=4711                # UID ohne passwd-Eintrag (verwaistes Home)

INSTALL=1
BROKEN_POOL=0

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mWARN:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mFEHLER:\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
	sed -n '3,15p' "$0" | sed 's/^# \{0,1\}//'
	exit "${1:-0}"
}

# --- Hilfsfunktionen -------------------------------------------------------

have_systemd() { [ -d /run/systemd/system ]; }

# record TYP PFAD: merkt ein angelegtes Objekt (doppelte Eintraege vermeiden)
record() {
	grep -qxF "$1 $2" "$MANIFEST" 2>/dev/null || echo "$1 $2" >>"$MANIFEST"
}

# put_file PFAD MODUS: schreibt stdin nach PFAD; existiert dort eine fremde
# Datei, wird sie vorher einmalig nach $ORIG_DIR gesichert.
put_file() {
	local path=$1 mode=${2:-0644}
	if [ -e "$path" ] && ! grep -qxF "file $path" "$MANIFEST" 2>/dev/null \
		&& ! grep -qxF "replaced $path" "$MANIFEST" 2>/dev/null; then
		mkdir -p "$ORIG_DIR$(dirname "$path")"
		cp -a "$path" "$ORIG_DIR$path"
		record replaced "$path"
	fi
	mkdir -p "$(dirname "$path")"
	cat >"$path"
	chmod "$mode" "$path"
	grep -qxF "replaced $path" "$MANIFEST" || record file "$path"
}

# make_dir PFAD [OWNER]: legt Verzeichnis an und merkt es
make_dir() {
	local path=$1 owner=${2:-}
	if [ ! -d "$path" ]; then
		mkdir -p "$path"
		record dir "$path"
	fi
	if [ -n "$owner" ]; then chown -R "$owner" "$path"; fi
}

# make_user NAME HOME SHELL [--no-home]
make_user() {
	local name=$1 home=$2 shell=$3 nohome=${4:-}
	if id "$name" >/dev/null 2>&1; then
		getent passwd "$name" | cut -d: -f5 | grep -q "$MARKER" \
			|| die "Nutzer $name existiert bereits und stammt nicht von $MARKER"
	else
		if [ "$nohome" = "--no-home" ]; then
			useradd -M -d "$home" -s "$shell" -c "$MARKER" "$name"
		else
			useradd -m -d "$home" -s "$shell" -c "$MARKER" "$name"
			record home "$home"
		fi
	fi
	record user "$name"
}

php_version() {
	local d
	for d in /etc/php/*/fpm/pool.d; do
		[ -d "$d" ] && { basename "$(dirname "$(dirname "$d")")"; return; }
	done
	return 1
}

reload_service() {
	local svc=$1
	if ! have_systemd; then
		warn "kein systemd aktiv, $svc wird nicht neu geladen"
		return 0
	fi
	systemctl list-unit-files "$svc.service" >/dev/null 2>&1 || return 0
	systemctl restart "$svc" || warn "$svc konnte nicht neu gestartet werden"
}

# --- Setup -----------------------------------------------------------------

install_packages() {
	log "Installiere Pakete (apache2, caddy, php-fpm, logrotate, cron)"
	export DEBIAN_FRONTEND=noninteractive
	apt-get update -qq
	# caddy belegt nach der Installation Port 80 wie Apache; das Caddyfile
	# wird weiter unten auf Port 8080 umgestellt.
	apt-get install -y -qq apache2 caddy php-fpm logrotate cron sudo >/dev/null \
		|| die "Paketinstallation fehlgeschlagen"
}

setup_users() {
	log "Lege Testnutzer und Homes an"
	# 1. alice: gesunder Nutzer mit Apache-Seite, PHP-Pool, logrotate, sudoers
	make_user ${PREFIX}alice /home/${PREFIX}alice /bin/bash
	# 2. bob: Caddy-Seite, PHP-Pool, Cron, systemd-Unit
	make_user ${PREFIX}bob /home/${PREFIX}bob /bin/bash
	# 3. carol: passwd-Eintrag, aber kein Home-Verzeichnis
	make_user ${PREFIX}carol /home/${PREFIX}carol /bin/bash --no-home
	# 4. frank: SFTP-/Webnutzer mit nologin, hat trotzdem eine aktive Seite
	make_user ${PREFIX}frank /home/${PREFIX}frank /usr/sbin/nologin
	# 5. web / webadmin: Teilstring-Falle (/home/uct-web vs. /home/uct-webadmin)
	make_user ${PREFIX}web /home/${PREFIX}web /bin/bash
	make_user ${PREFIX}webadmin /home/${PREFIX}webadmin /bin/bash
	# 6. gina: Home ausserhalb von /home, direkt im Webroot
	make_user ${PREFIX}gina /var/www/${PREFIX}gina /usr/sbin/nologin

	# Inhalte, damit Backups etwas zu sichern haben
	local u
	for u in alice bob frank web webadmin gina; do
		local h; h=$(getent passwd "${PREFIX}$u" | cut -d: -f6)
		mkdir -p "$h/.ssh"
		echo "ssh-ed25519 AAAATEST ${PREFIX}$u@testenv" >"$h/.ssh/authorized_keys"
		echo "DB_PASSWORD=geheim-${u}" >"$h/.env"
		chmod 700 "$h/.ssh"; chmod 600 "$h/.ssh/authorized_keys" "$h/.env"
		chown -R "${PREFIX}$u:${PREFIX}$u" "$h"
	done

	# 7. dave: verwaistes Home, kein passwd-Eintrag, Dateien gehoeren UID 4711
	log "Lege verwaistes Home /home/${PREFIX}dave an (UID $ORPHAN_UID ohne Nutzer)"
	make_dir /home/${PREFIX}dave
	echo "Reste eines geloeschten Nutzers" >/home/${PREFIX}dave/notizen.txt
	mkdir -p /home/${PREFIX}dave/public_html
	echo "<?php echo 'dave';" >/home/${PREFIX}dave/public_html/index.php
	chown -R $ORPHAN_UID:$ORPHAN_UID /home/${PREFIX}dave
	record home /home/${PREFIX}dave
}

setup_webroots() {
	log "Lege Webroots unter /var/www an"
	local u
	for u in alice bob frank web webadmin; do
		make_dir /var/www/$u.$DOMAIN
		printf '<?php echo "%s ok, PHP " . PHP_VERSION;\n' "$u" \
			>/var/www/$u.$DOMAIN/index.php
		chown -R "${PREFIX}$u:${PREFIX}$u" /var/www/$u.$DOMAIN
	done
	make_dir /var/www/${PREFIX}gina/htdocs "${PREFIX}gina:${PREFIX}gina"
	echo '<?php echo "gina ok";' >/var/www/${PREFIX}gina/htdocs/index.php
	chown -R "${PREFIX}gina:${PREFIX}gina" /var/www/${PREFIX}gina/htdocs
	chmod o+x /var/www/${PREFIX}gina   # useradd legt das Home mit 0750 an
	# erin: Seite existiert nur noch in den Configs, Webroot fehlt absichtlich
}

setup_php() {
	local ver
	if ! ver=$(php_version); then
		warn "kein PHP-FPM gefunden, Pools werden uebersprungen"
		return 0
	fi
	log "Lege PHP-FPM-Pools fuer PHP $ver an"
	local pooldir=/etc/php/$ver/fpm/pool.d u
	for u in alice bob frank web gina; do
		put_file $pooldir/${PREFIX}$u.conf <<-EOF
		; $MARKER
		[${PREFIX}$u]
		user = ${PREFIX}$u
		group = ${PREFIX}$u
		listen = /run/php/${PREFIX}$u.sock
		listen.owner = www-data
		listen.group = www-data
		pm = ondemand
		pm.max_children = 2
		php_admin_value[open_basedir] = /var/www/:/tmp/
		php_admin_value[error_log] = /var/log/${PREFIX}$u/php-error.log
		EOF
		make_dir /var/log/${PREFIX}$u "${PREFIX}$u:${PREFIX}$u"
	done
	if [ "$BROKEN_POOL" = 1 ]; then
		# Pool fuer einen geloeschten Nutzer: php-fpm startet damit NICHT mehr.
		warn "lege defekten Pool fuer nicht existierenden Nutzer ${PREFIX}erin an"
		put_file $pooldir/${PREFIX}erin.conf <<-EOF
		; $MARKER - Nutzer existiert nicht (Problemfall)
		[${PREFIX}erin]
		user = ${PREFIX}erin
		group = ${PREFIX}erin
		listen = /run/php/${PREFIX}erin.sock
		listen.owner = www-data
		listen.group = www-data
		pm = ondemand
		pm.max_children = 2
		EOF
	fi
	reload_service php"$ver"-fpm
}

setup_apache() {
	[ -d /etc/apache2 ] || { warn "kein Apache gefunden, uebersprungen"; return 0; }
	log "Lege Apache-vhosts an"
	local u
	for u in alice frank web webadmin; do
		local pool=${PREFIX}$u
		[ "$u" = webadmin ] && pool=${PREFIX}web   # teilt sich den Pool mit web
		put_file /etc/apache2/sites-available/${PREFIX}$u.conf <<-EOF
		# $MARKER
		<VirtualHost *:80>
		    ServerName $u.$DOMAIN
		    ServerAlias www.$u.$DOMAIN
		    DocumentRoot /var/www/$u.$DOMAIN
		    <Directory /var/www/$u.$DOMAIN>
		        Require all granted
		    </Directory>
		    <FilesMatch "\.php$">
		        SetHandler "proxy:unix:/run/php/$pool.sock|fcgi://localhost"
		    </FilesMatch>
		    ErrorLog \${APACHE_LOG_DIR}/${PREFIX}$u-error.log
		    CustomLog \${APACHE_LOG_DIR}/${PREFIX}$u-access.log combined
		</VirtualHost>
		EOF
	done
	# gina: DocumentRoot im Home ausserhalb von /home
	put_file /etc/apache2/sites-available/${PREFIX}gina.conf <<-EOF
	# $MARKER
	<VirtualHost *:80>
	    ServerName gina.$DOMAIN
	    DocumentRoot /var/www/${PREFIX}gina/htdocs
	    <Directory /var/www/${PREFIX}gina/htdocs>
	        Require all granted
	    </Directory>
	    <FilesMatch "\.php$">
	        SetHandler "proxy:unix:/run/php/${PREFIX}gina.sock|fcgi://localhost"
	    </FilesMatch>
	</VirtualHost>
	EOF
	# erin: Nutzer geloescht, vhost aktiv, DocumentRoot fehlt
	put_file /etc/apache2/sites-available/${PREFIX}erin.conf <<-EOF
	# $MARKER - Nutzer und Webroot existieren nicht (Problemfall)
	<VirtualHost *:80>
	    ServerName erin.$DOMAIN
	    DocumentRoot /var/www/erin.$DOMAIN
	    <FilesMatch "\.php$">
	        SetHandler "proxy:unix:/run/php/${PREFIX}erin.sock|fcgi://localhost"
	    </FilesMatch>
	</VirtualHost>
	EOF
	# dave: vhost nur in sites-available (deaktiviert), zeigt ins verwaiste Home
	put_file /etc/apache2/sites-available/${PREFIX}dave.conf <<-EOF
	# $MARKER - deaktiviert, Nutzer existiert nicht mehr (Problemfall)
	<VirtualHost *:80>
	    ServerName dave.$DOMAIN
	    DocumentRoot /home/${PREFIX}dave/public_html
	</VirtualHost>
	EOF

	a2enmod -q proxy_fcgi setenvif >/dev/null
	for u in alice frank web webadmin gina erin; do
		if [ ! -e /etc/apache2/sites-enabled/${PREFIX}$u.conf ]; then
			a2ensite -q ${PREFIX}$u >/dev/null
		fi
		record link /etc/apache2/sites-enabled/${PREFIX}$u.conf
	done
	apache2ctl configtest 2>&1 | sed 's/^/    apache: /' || true
	reload_service apache2
}

setup_caddy() {
	[ -d /etc/caddy ] || { warn "kein Caddy gefunden, uebersprungen"; return 0; }
	log "Lege Caddy-Sites an (Port 8080, damit Apache Port 80 behaelt)"
	put_file /etc/caddy/Caddyfile <<-EOF
	# $MARKER - Original liegt in $ORIG_DIR/etc/caddy/Caddyfile
	{
	    auto_https off
	}
	import /etc/caddy/sites.d/*.caddy
	EOF
	make_dir /etc/caddy/sites.d
	put_file /etc/caddy/sites.d/${PREFIX}bob.caddy <<-EOF
	# $MARKER
	http://bob.$DOMAIN:8080, http://www.bob.$DOMAIN:8080 {
	    root * /var/www/bob.$DOMAIN
	    php_fastcgi unix//run/php/${PREFIX}bob.sock
	    file_server
	    log {
	        output file /var/log/caddy/${PREFIX}bob.log
	    }
	}
	EOF
	# web zusaetzlich auch unter Caddy (Nutzer mit Seiten in zwei Webservern)
	put_file /etc/caddy/sites.d/${PREFIX}web.caddy <<-EOF
	# $MARKER
	http://web.$DOMAIN:8080 {
	    root * /var/www/web.$DOMAIN
	    php_fastcgi unix//run/php/${PREFIX}web.sock
	    file_server
	}
	EOF
	# erin: Site auf nicht vorhandenes Verzeichnis
	put_file /etc/caddy/sites.d/${PREFIX}erin.caddy <<-EOF
	# $MARKER - Webroot existiert nicht (Problemfall)
	http://erin.$DOMAIN:8080 {
	    root * /var/www/erin.$DOMAIN
	    file_server
	}
	EOF
	# Reverse-Proxy auf einen Dienst von bob
	put_file /etc/caddy/sites.d/${PREFIX}bob-app.caddy <<-EOF
	# $MARKER
	http://app.bob.$DOMAIN:8080 {
	    reverse_proxy 127.0.0.1:9101
	}
	EOF
	if command -v caddy >/dev/null; then
		caddy validate --adapter caddyfile --config /etc/caddy/Caddyfile 2>&1 \
			| tail -1 | sed 's/^/    caddy: /' || true
	fi
	reload_service caddy
}

setup_misc() {
	log "Lege logrotate, Cron, sudoers und systemd-Unit an"
	local u
	for u in alice bob frank web gina erin; do
		put_file /etc/logrotate.d/${PREFIX}$u <<-EOF
		# $MARKER
		/var/log/${PREFIX}$u/*.log {
		    weekly
		    rotate 4
		    missingok
		    notifempty
		    compress
		    su ${PREFIX}$u ${PREFIX}$u
		    create 0640 ${PREFIX}$u ${PREFIX}$u
		}
		EOF
	done
	# erin: logrotate fuer einen nicht existierenden Nutzer -> logrotate-Fehler

	put_file /etc/cron.d/${PREFIX}bob <<-EOF
	# $MARKER
	*/15 * * * * ${PREFIX}bob test -x /home/${PREFIX}bob/bin/job.sh && /home/${PREFIX}bob/bin/job.sh
	EOF
	if [ -d /var/spool/cron/crontabs ]; then
		put_file /var/spool/cron/crontabs/${PREFIX}alice 0600 <<-EOF
		# $MARKER
		0 3 * * * php /var/www/alice.$DOMAIN/index.php >/dev/null
		EOF
		chown ${PREFIX}alice:crontab /var/spool/cron/crontabs/${PREFIX}alice 2>/dev/null || true
	fi

	put_file /etc/sudoers.d/${PREFIX}alice 0440 <<-EOF
	# $MARKER
	${PREFIX}alice ALL=(root) NOPASSWD: /usr/sbin/apache2ctl graceful
	EOF
	if command -v visudo >/dev/null; then
		visudo -cf /etc/sudoers.d/${PREFIX}alice >/dev/null \
			|| die "sudoers-Datei ungueltig"
	fi

	put_file /etc/systemd/system/${PREFIX}bob-app.service <<-EOF
	# $MARKER
	[Unit]
	Description=Testdienst von ${PREFIX}bob (nicht gestartet)

	[Service]
	User=${PREFIX}bob
	Group=${PREFIX}bob
	WorkingDirectory=/home/${PREFIX}bob
	ExecStart=/usr/bin/sleep infinity

	[Install]
	WantedBy=multi-user.target
	EOF
	if have_systemd; then systemctl daemon-reload; fi

	# /etc/hosts, damit die Testdomains lokal aufloesen
	if ! grep -q "# $MARKER" /etc/hosts; then
		put_file_hosts
	fi
}

put_file_hosts() {
	local names="" u
	for u in alice bob frank web webadmin gina erin dave; do
		names="$names $u.$DOMAIN"
	done
	names="$names www.alice.$DOMAIN www.bob.$DOMAIN app.bob.$DOMAIN"
	echo "127.0.0.1$names # $MARKER" >>/etc/hosts
	record hostsline /etc/hosts
}

cmd_setup() {
	mkdir -p "$STATE_DIR" "$ORIG_DIR"
	chmod 700 "$STATE_DIR"
	touch "$MANIFEST"
	[ "$INSTALL" = 1 ] && install_packages
	setup_users
	setup_webroots
	setup_php
	setup_apache
	setup_caddy
	setup_misc
	log "Fertig. Uebersicht: $0 status"
}

# --- Teardown --------------------------------------------------------------

# beendet alle Prozesse eines Nutzers (z. B. PHP-FPM-Worker), notfalls mit KILL
stop_user_processes() {
	local i
	pkill -u "$1" 2>/dev/null || return 0
	for i in 1 2 3 4 5; do
		pgrep -u "$1" >/dev/null || return 0
		sleep 1
	done
	pkill -KILL -u "$1" 2>/dev/null || true
	sleep 1
}

cmd_teardown() {
	[ -f "$MANIFEST" ] || die "kein Manifest unter $MANIFEST, nichts abzubauen"
	log "Baue Testumgebung laut $MANIFEST ab"
	local type path failed=0

	# Symlinks und Dateien zuerst (Configs vor Nutzern entfernen)
	while read -r type path; do
		case $type in
			link) rm -f "$path" ;;
			file) rm -f "$path" ;;
			replaced)
				cp -a "$ORIG_DIR$path" "$path" && log "Original wiederhergestellt: $path" ;;
			hostsline) sed -i "/# $MARKER\$/d" "$path" ;;
		esac
	done <"$MANIFEST"

	if have_systemd; then systemctl daemon-reload || true; fi
	local ver
	if ver=$(php_version); then reload_service php"$ver"-fpm; fi
	[ -d /etc/apache2 ] && reload_service apache2
	[ -d /etc/caddy ] && reload_service caddy

	# Nutzer (nur mit Marker), danach Homes und Verzeichnisse
	while read -r type path; do
		[ "$type" = user ] || continue
		if getent passwd "$path" | cut -d: -f5 | grep -q "$MARKER"; then
			stop_user_processes "$path"
			if userdel "$path"; then
				log "Nutzer entfernt: $path"
			else
				warn "Nutzer $path konnte nicht entfernt werden"
				failed=1
			fi
		fi
	done <"$MANIFEST"

	# Verzeichnisse in umgekehrter Reihenfolge, nur unterhalb erwarteter Pfade
	tac "$MANIFEST" | while read -r type path; do
		case $type in
			home|dir)
				case $path in
					/home/${PREFIX}*|/var/www/*|/var/log/${PREFIX}*|/etc/caddy/sites.d)
						rm -rf -- "$path" ;;
					*) warn "uebersprungen (unerwarteter Pfad): $path" ;;
				esac ;;
		esac
	done

	if [ "$failed" = 1 ]; then
		warn "Abbau unvollstaendig, Manifest bleibt erhalten: $0 teardown erneut ausfuehren"
		exit 1
	fi
	rm -rf -- "$STATE_DIR"
	log "Abgebaut. Installierte Pakete wurden nicht entfernt."
}

# --- Status ----------------------------------------------------------------

cmd_status() {
	echo "Testnutzer (GECOS = $MARKER):"
	getent passwd | awk -F: -v m="$MARKER" '$5==m {printf "  %-14s uid=%-6s home=%-24s shell=%s\n",$1,$3,$6,$7}'
	echo
	echo "Homes ohne passwd-Eintrag:"
	local d
	for d in /home/*; do
		[ -d "$d" ] || continue
		getent passwd | cut -d: -f6 | grep -qxF "$d" || echo "  $d (Besitzer-UID $(stat -c %u "$d"))"
	done
	echo
	echo "Problemfaelle im Aufbau:"
	cat <<-EOF
	  ${PREFIX}carol      passwd-Eintrag ohne Home
	  ${PREFIX}dave       verwaistes Home + deaktivierter Apache-vhost
	  ${PREFIX}erin       Nutzer fehlt, aber Apache-vhost, Caddy-Site, logrotate$( [ -f /etc/php/*/fpm/pool.d/${PREFIX}erin.conf ] 2>/dev/null && echo ", PHP-Pool" )
	  ${PREFIX}frank      nologin-Shell, aber aktive Seite
	  ${PREFIX}gina       Home liegt in /var/www (userdel -r waere gefaehrlich)
	  ${PREFIX}web/-webadmin  Teilstring-Falle bei Namen und Pfaden
	EOF
	echo
	if [ -f "$MANIFEST" ]; then
		echo "Manifest: $(wc -l <"$MANIFEST") Eintraege in $MANIFEST"
	else
		echo "Kein Manifest gefunden: Umgebung ist nicht aufgebaut."
	fi
}

# --- Main ------------------------------------------------------------------

[ $# -ge 1 ] || usage 1
cmd=$1; shift
while [ $# -gt 0 ]; do
	case $1 in
		--no-install) INSTALL=0 ;;
		--with-broken-pool) BROKEN_POOL=1 ;;
		-h|--help) usage ;;
		*) die "unbekannte Option: $1" ;;
	esac
	shift
done

[ "$(id -u)" = 0 ] || die "bitte als root ausfuehren (sudo)"
[ -f /etc/debian_version ] || die "nur fuer Debian/Ubuntu gedacht"

case $cmd in
	setup) cmd_setup ;;
	teardown) cmd_teardown ;;
	status) cmd_status ;;
	-h|--help|help) usage ;;
	*) usage 1 ;;
esac
