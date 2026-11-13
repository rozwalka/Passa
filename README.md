# Passa: instalacja na Pixelu (jednorazowo, ok. 5 minut)

1. Załóż darmowe konto na github.com (jeśli nie masz).
2. Kliknij **New repository**, nazwij je np. `passa`, zaznacz **Public**, utwórz.
3. W repozytorium kliknij **Add file → Upload files** i wrzuć WSZYSTKIE pliki z tego folderu
   (index.html, sw.js, manifest.webmanifest, trzy pliki icon-*.png). Kliknij **Commit changes**.
4. Wejdź w **Settings → Pages**. W „Branch” wybierz `main` i folder `/ (root)`, kliknij **Save**.
5. Po 1–2 minutach apka będzie pod adresem `https://TWOJ-LOGIN.github.io/passa/`.
6. Otwórz ten adres w Chrome na Pixelu → menu ⋮ → **Zainstaluj aplikację** (albo „Dodaj do ekranu głównego”).

Od teraz Passa ma ikonkę i działa bez internetu. Dane są tylko w telefonie.

## Kopia zapasowa
Ustawienia (zębatka) → **Zapisz kopię zapasową**. Plik ląduje w Pobranych.
Odinstalowanie apki albo wyczyszczenie danych Chrome kasuje dane, więc rób kopię co jakiś czas.

## Aktualizacja
Podmień pliki w repozytorium. Apka pobierze nową wersję sama przy następnym otwarciu z internetem
(czasem trzeba ją otworzyć dwa razy). Dane zostają.

## Zdjęcia ćwiczeń
Obrazki w folderze img pochodzą z bazy free-exercise-db (github.com/yuhonas/free-exercise-db), udostępnionej jako domena publiczna (Unlicense).

## Serwer na Macu (konta, synchronizacja, AI)
1. Wgraj na GitHuba folder `server` (install.sh i pliki passa-server-…).
2. Na Macu w Terminalu: `curl -fsSL https://rozwalka.github.io/Passa/server/install.sh | bash`
3. Jeśli pojawi się link Tailscale, otwórz go i kliknij Enable.
4. Na końcu instalator pokaże kod zaproszenia. W Passie: Ustawienia → Zaloguj się albo załóż konto.

Dane kont są na Macu w `~/passa-server/data`, codzienne kopie w `~/passa-server/data/backups` (14 dni).
Polecenia: `~/passa-server/passa-server users | reset-password LOGIN | new-invite | delete-user LOGIN`.
Kod źródłowy serwera: `server/main.go`.
