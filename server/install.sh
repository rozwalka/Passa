#!/bin/bash
# Instalator serwera Passa na Macu.
# Uruchom w Terminalu:  curl -fsSL https://rozwalka.github.io/Passa/server/install.sh | bash

BASE="https://rozwalka.github.io/Passa/server"
DIR="$HOME/passa-server"
LA="$HOME/Library/LaunchAgents"
TS="/Applications/Tailscale.app/Contents/MacOS/Tailscale"
OLLAMA_BIN="/Applications/Ollama.app/Contents/Resources/ollama"
PORT=8787
UIDN=$(id -u)

say(){ printf "\n\033[1;33m▸ %s\033[0m\n" "$1"; }
ok(){ printf "  \033[1;32m✓\033[0m %s\n" "$1"; }
bad(){ printf "  \033[1;31m✗ %s\033[0m\n" "$1"; }

say "1/5 Pobieram serwer Passa"
mkdir -p "$DIR" "$LA"
ARCH=$(uname -m)
if ! curl -fL --progress-bar -o "$DIR/passa-server.new" "$BASE/passa-server-$ARCH"; then
  bad "Nie udało się pobrać $BASE/passa-server-$ARCH. Sprawdź, czy folder server jest wgrany na GitHuba."
  exit 1
fi
chmod +x "$DIR/passa-server.new"
xattr -d com.apple.quarantine "$DIR/passa-server.new" 2>/dev/null
mv -f "$DIR/passa-server.new" "$DIR/passa-server"
ok "$("$DIR/passa-server" version)"

say "2/5 Ustawiam Ollamę tylko na tym Macu (AI idzie przez serwer Passa, za loginem)"
launchctl unsetenv OLLAMA_HOST 2>/dev/null
launchctl unsetenv OLLAMA_ORIGINS 2>/dev/null
if [ -x "$OLLAMA_BIN" ]; then
  launchctl unload "$LA/pl.passa.ollama.plist" 2>/dev/null
  pkill -9 -x Ollama 2>/dev/null
  pkill -9 -x ollama 2>/dev/null
  sleep 1
  cat > "$LA/pl.passa.ollama.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>pl.passa.ollama</string>
  <key>ProgramArguments</key>
  <array><string>$OLLAMA_BIN</string><string>serve</string></array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>OLLAMA_HOST</key><string>127.0.0.1:11434</string>
    <key>OLLAMA_KEEP_ALIVE</key><string>30m</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/tmp/ollama-passa.log</string>
  <key>StandardErrorPath</key><string>/tmp/ollama-passa.log</string>
</dict>
</plist>
EOF
  launchctl load -w "$LA/pl.passa.ollama.plist"
  ok "Ollama działa w tle tylko na tym Macu"
else
  bad "Nie znalazłem Ollamy w Aplikacjach. Serwer zadziała, ale AI nie."
fi

say "3/5 Uruchamiam serwer Passa w tle"
launchctl unload "$LA/pl.passa.server.plist" 2>/dev/null
cat > "$LA/pl.passa.server.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>pl.passa.server</string>
  <key>ProgramArguments</key>
  <array><string>$DIR/passa-server</string><string>-addr</string><string>127.0.0.1:$PORT</string><string>serve</string></array>
  <key>WorkingDirectory</key><string>$DIR</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$DIR/server.log</string>
  <key>StandardErrorPath</key><string>$DIR/server.log</string>
</dict>
</plist>
EOF
launchctl load -w "$LA/pl.passa.server.plist"
for i in 1 2 3 4 5 6 7 8 9 10; do
  if curl -fs "http://127.0.0.1:$PORT/api/health" >/dev/null; then break; fi
  sleep 1
done
if curl -fs "http://127.0.0.1:$PORT/api/health" >/dev/null; then
  ok "Serwer działa: $(curl -fs http://127.0.0.1:$PORT/api/health)"
else
  bad "Serwer nie odpowiada. Ostatnie linijki logu:"; tail -5 "$DIR/server.log"; exit 1
fi

say "4/5 Wystawiam serwer do internetu przez Tailscale Funnel"
if [ -x "$TS" ]; then
  "$TS" serve --https=443 off >/dev/null 2>&1 </dev/null
  echo "  Jeśli niżej pojawi się link, otwórz go w przeglądarce i kliknij Enable. Polecenie poczeka."
  "$TS" funnel --bg $PORT </dev/null
  HOSTNAME_TS=$("$TS" status --json 2>/dev/null | grep -o '"DNSName": *"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/' | sed 's/\.$//')
else
  bad "Nie znalazłem Tailscale w Aplikacjach."
fi

say "5/5 Gotowe"
echo "  Adres serwera:     https://${HOSTNAME_TS:-mac-mini-rozwalka.tail9f6d92.ts.net}"
echo "  $("$DIR/passa-server" invite)"
echo
echo "  W Passie: Ustawienia (zębatka) → Zaloguj się albo załóż konto → Załóż konto, wpisz kod zaproszenia."
echo "  Aplikację Ollama (lama) zostaw zamkniętą. Wszystko działa w tle i startuje samo po restarcie Maca."
echo
echo "  Przydatne polecenia:"
echo "    ~/passa-server/passa-server users                 lista kont"
echo "    ~/passa-server/passa-server reset-password LOGIN  nowe hasło dla konta"
echo "    ~/passa-server/passa-server new-invite            nowy kod zaproszenia"
echo "    tail -20 ~/passa-server/server.log                log serwera"
