// Passa server: konta, synchronizacja danych i pośrednik do Ollamy.
// Działa na Macu w tle, wystawiany przez Tailscale Funnel.
package main

import (
	"bufio"
	"bytes"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const version = "1.0.0"

var (
	addr      = flag.String("addr", "127.0.0.1:8787", "adres nasłuchu")
	dataDir   = flag.String("data", defaultDataDir(), "folder z danymi")
	ollamaURL = flag.String("ollama", "http://127.0.0.1:11434", "adres Ollamy")
	origins   = flag.String("origins", "https://rozwalka.github.io", "dozwolone strony (po przecinku)")
	maxUsers  = flag.Int("max-users", 15, "maksymalna liczba kont")
)

func defaultDataDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "passa-data"
	}
	return filepath.Join(h, "passa-server", "data")
}

/* ================= model ================= */

type User struct {
	ID       string    `json:"id"`
	Login    string    `json:"login"`
	Salt     string    `json:"salt"`
	Hash     string    `json:"hash"`
	Iter     int       `json:"iter"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"lastSeen"`
}

type Session struct {
	UserID   string    `json:"user"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"lastSeen"`
	Device   string    `json:"device"`
}

type Config struct {
	Invite string `json:"invite"`
}

type Store struct {
	mu       sync.Mutex
	dir      string
	users    map[string]*User    // by login
	sessions map[string]*Session // by sha256(token)
	cfg      Config
	dataMu   sync.Map // per-user mutex
}

const pbkdfIter = 310000

func hashPassword(pw string, salt []byte, iter int) string {
	k, err := pbkdf2.Key(sha256.New, pw, salt, iter, 32)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(k)
}

func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func tokenKey(tok string) string { s := sha256.Sum256([]byte(tok)); return hex.EncodeToString(s[:]) }

func newInvite() string {
	const abc = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	rand.Read(b)
	out := make([]byte, 0, 15)
	out = append(out, "PASSA-"...)
	for i, x := range b {
		if i == 4 {
			out = append(out, '-')
		}
		out = append(out, abc[int(x)%len(abc)])
	}
	return string(out)
}

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSONFile(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func openStore(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "users"), 0700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, users: map[string]*User{}, sessions: map[string]*Session{}}
	var us []*User
	if err := readJSONFile(filepath.Join(dir, "accounts.json"), &us); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("accounts.json: %w", err)
	}
	for _, u := range us {
		s.users[u.Login] = u
	}
	if err := readJSONFile(filepath.Join(dir, "sessions.json"), &s.sessions); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("sessions.json: %w", err)
	}
	if s.sessions == nil {
		s.sessions = map[string]*Session{}
	}
	if err := readJSONFile(filepath.Join(dir, "config.json"), &s.cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("config.json: %w", err)
	}
	if s.cfg.Invite == "" {
		s.cfg.Invite = newInvite()
		if err := writeJSONFile(filepath.Join(dir, "config.json"), s.cfg); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) saveUsersLocked() error {
	us := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		us = append(us, u)
	}
	sort.Slice(us, func(i, j int) bool { return us[i].Created.Before(us[j].Created) })
	return writeJSONFile(filepath.Join(s.dir, "accounts.json"), us)
}
func (s *Store) saveSessionsLocked() error {
	return writeJSONFile(filepath.Join(s.dir, "sessions.json"), s.sessions)
}

func (s *Store) userByID(id string) *User {
	for _, u := range s.users {
		if u.ID == id {
			return u
		}
	}
	return nil
}

func (s *Store) userLock(id string) *sync.Mutex {
	m, _ := s.dataMu.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex)
}

type UserData struct {
	Rev     int64           `json:"rev"`
	Updated time.Time       `json:"updated"`
	Data    json.RawMessage `json:"data"`
}

func (s *Store) dataPath(id string) string { return filepath.Join(s.dir, "users", id+".json") }

/* ================= limity prób ================= */

type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{hits: map[string][]time.Time{}} }

// allow zwraca false, gdy w oknie było już max prób
func (l *limiter) count(key string, window time.Duration) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	h := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < window {
			h = append(h, t)
		}
	}
	l.hits[key] = h
	return len(h)
}
func (l *limiter) add(key string) {
	l.mu.Lock()
	l.hits[key] = append(l.hits[key], time.Now())
	l.mu.Unlock()
}
func (l *limiter) reset(key string) { l.mu.Lock(); delete(l.hits, key); l.mu.Unlock() }

/* ================= HTTP ================= */

type Server struct {
	st      *Store
	fails   *limiter
	regs    *limiter
	aiHits  *limiter
	aiSem   chan struct{}
	origins map[string]bool
}

var loginRe = regexp.MustCompile(`^[a-z0-9._-]{3,32}$`)

func clientIP(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "127.0.0.1" || host == "::1" {
		if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
			return strings.TrimSpace(strings.Split(xf, ",")[0])
		}
	}
	return host
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, code int, msg string) { writeJSON(w, code, map[string]string{"error": msg}) }

func (sv *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && sv.origins[o] {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func readBody(r *http.Request, max int64, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, max))
	return dec.Decode(v)
}

func (sv *Server) auth(r *http.Request) (*User, string) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return nil, ""
	}
	k := tokenKey(strings.TrimPrefix(h, "Bearer "))
	st := sv.st
	st.mu.Lock()
	defer st.mu.Unlock()
	ses := st.sessions[k]
	if ses == nil {
		return nil, ""
	}
	if time.Since(ses.LastSeen) > 120*24*time.Hour {
		delete(st.sessions, k)
		st.saveSessionsLocked()
		return nil, ""
	}
	u := st.userByID(ses.UserID)
	if u == nil {
		return nil, ""
	}
	if time.Since(ses.LastSeen) > time.Hour {
		ses.LastSeen = time.Now()
		u.LastSeen = ses.LastSeen
		st.saveSessionsLocked()
		st.saveUsersLocked()
	}
	return u, k
}

func (sv *Server) newSession(u *User, device string) (string, error) {
	tok := randToken(32)
	st := sv.st
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	st.sessions[tokenKey(tok)] = &Session{UserID: u.ID, Created: now, LastSeen: now, Device: device}
	u.LastSeen = now
	st.saveUsersLocked()
	return tok, st.saveSessionsLocked()
}

type credReq struct {
	Login    string `json:"login"`
	Password string `json:"password"`
	Invite   string `json:"invite"`
	Device   string `json:"device"`
}

func (sv *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if sv.regs.count("reg:"+ip, time.Hour) >= 5 || sv.fails.count("ip:"+ip, 15*time.Minute) >= 20 {
		fail(w, 429, "Za dużo prób. Spróbuj za kilkanaście minut.")
		return
	}
	var c credReq
	if err := readBody(r, 4096, &c); err != nil {
		fail(w, 400, "Złe dane.")
		return
	}
	c.Login = strings.ToLower(strings.TrimSpace(c.Login))
	st := sv.st
	if subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(c.Invite))), []byte(st.cfg.Invite)) != 1 {
		sv.regs.add("reg:" + ip)
		sv.fails.add("ip:" + ip)
		fail(w, 403, "Zły kod zaproszenia.")
		return
	}
	if !loginRe.MatchString(c.Login) {
		fail(w, 400, "Login: 3–32 znaki, małe litery, cyfry, kropka, myślnik albo podkreślnik.")
		return
	}
	if len([]rune(c.Password)) < 8 || len(c.Password) > 200 {
		fail(w, 400, "Hasło musi mieć co najmniej 8 znaków.")
		return
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	st.mu.Lock()
	if _, ok := st.users[c.Login]; ok {
		st.mu.Unlock()
		fail(w, 409, "Taki login już istnieje.")
		return
	}
	if len(st.users) >= *maxUsers {
		st.mu.Unlock()
		fail(w, 403, fmt.Sprintf("Na tym serwerze jest już maksymalnie %d kont.", *maxUsers))
		return
	}
	u := &User{ID: randToken(9), Login: c.Login, Salt: hex.EncodeToString(salt), Iter: pbkdfIter, Created: time.Now()}
	u.Hash = hashPassword(c.Password, salt, u.Iter)
	st.users[u.Login] = u
	err := st.saveUsersLocked()
	st.mu.Unlock()
	if err != nil {
		fail(w, 500, "Nie udało się zapisać konta.")
		return
	}
	sv.regs.add("reg:" + ip)
	log.Printf("nowe konto: %s (%s)", u.Login, ip)
	tok, _ := sv.newSession(u, c.Device)
	writeJSON(w, 200, map[string]any{"token": tok, "login": u.Login})
}

func (sv *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	var c credReq
	if err := readBody(r, 4096, &c); err != nil {
		fail(w, 400, "Złe dane.")
		return
	}
	c.Login = strings.ToLower(strings.TrimSpace(c.Login))
	if sv.fails.count("ip:"+ip, 15*time.Minute) >= 20 || sv.fails.count("u:"+c.Login, 15*time.Minute) >= 5 {
		fail(w, 429, "Za dużo nieudanych prób. Poczekaj 15 minut.")
		return
	}
	st := sv.st
	st.mu.Lock()
	u := st.users[c.Login]
	st.mu.Unlock()
	ok := false
	if u != nil {
		salt, _ := hex.DecodeString(u.Salt)
		ok = subtle.ConstantTimeCompare([]byte(hashPassword(c.Password, salt, u.Iter)), []byte(u.Hash)) == 1
	} else {
		hashPassword(c.Password, []byte("passa-dummy-salt"), pbkdfIter) // ten sam czas odpowiedzi
	}
	if !ok {
		sv.fails.add("ip:" + ip)
		sv.fails.add("u:" + c.Login)
		log.Printf("nieudane logowanie: %q (%s)", c.Login, ip)
		fail(w, 401, "Zły login albo hasło.")
		return
	}
	sv.fails.reset("u:" + c.Login)
	tok, err := sv.newSession(u, c.Device)
	if err != nil {
		fail(w, 500, "Błąd serwera.")
		return
	}
	writeJSON(w, 200, map[string]any{"token": tok, "login": u.Login})
}

func (sv *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	_, k := sv.auth(r)
	if k != "" {
		st := sv.st
		st.mu.Lock()
		delete(st.sessions, k)
		st.saveSessionsLocked()
		st.mu.Unlock()
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (sv *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	u, k := sv.auth(r)
	if u == nil {
		fail(w, 401, "Zaloguj się ponownie.")
		return
	}
	var c struct{ Old, New string }
	if err := readBody(r, 4096, &c); err != nil {
		fail(w, 400, "Złe dane.")
		return
	}
	if sv.fails.count("u:"+u.Login, 15*time.Minute) >= 5 {
		fail(w, 429, "Za dużo prób. Poczekaj 15 minut.")
		return
	}
	salt, _ := hex.DecodeString(u.Salt)
	if subtle.ConstantTimeCompare([]byte(hashPassword(c.Old, salt, u.Iter)), []byte(u.Hash)) != 1 {
		sv.fails.add("u:" + u.Login)
		fail(w, 403, "Obecne hasło się nie zgadza.")
		return
	}
	if len([]rune(c.New)) < 8 || len(c.New) > 200 {
		fail(w, 400, "Nowe hasło musi mieć co najmniej 8 znaków.")
		return
	}
	ns := make([]byte, 16)
	rand.Read(ns)
	st := sv.st
	st.mu.Lock()
	u.Salt, u.Iter = hex.EncodeToString(ns), pbkdfIter
	u.Hash = hashPassword(c.New, ns, u.Iter)
	for key, s := range st.sessions { // wyloguj inne urządzenia
		if s.UserID == u.ID && key != k {
			delete(st.sessions, key)
		}
	}
	st.saveUsersLocked()
	st.saveSessionsLocked()
	st.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (sv *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	u, _ := sv.auth(r)
	if u == nil {
		fail(w, 401, "Zaloguj się ponownie.")
		return
	}
	lk := sv.st.userLock(u.ID)
	lk.Lock()
	defer lk.Unlock()
	var cur UserData
	if err := readJSONFile(sv.st.dataPath(u.ID), &cur); err != nil && !errors.Is(err, os.ErrNotExist) {
		fail(w, 500, "Nie udało się odczytać danych.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("since") == fmt.Sprint(cur.Rev) {
			writeJSON(w, 200, map[string]any{"rev": cur.Rev, "same": true})
			return
		}
		writeJSON(w, 200, map[string]any{"rev": cur.Rev, "data": cur.Data})
	case http.MethodPut:
		var in struct {
			BaseRev int64           `json:"baseRev"`
			Force   bool            `json:"force"`
			Data    json.RawMessage `json:"data"`
		}
		if err := readBody(r, 20<<20, &in); err != nil {
			fail(w, 400, "Dane są uszkodzone albo za duże.")
			return
		}
		if len(in.Data) == 0 || in.Data[0] != '{' {
			fail(w, 400, "Dane są uszkodzone.")
			return
		}
		if !in.Force && in.BaseRev != cur.Rev {
			writeJSON(w, 409, map[string]any{"rev": cur.Rev, "data": cur.Data})
			return
		}
		nd := UserData{Rev: cur.Rev + 1, Updated: time.Now(), Data: in.Data}
		if err := writeJSONFile(sv.st.dataPath(u.ID), nd); err != nil {
			fail(w, 500, "Nie udało się zapisać danych.")
			return
		}
		writeJSON(w, 200, map[string]any{"rev": nd.Rev})
	default:
		fail(w, 405, "Zła metoda.")
	}
}

func (sv *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, _ := sv.auth(r)
	if u == nil {
		fail(w, 401, "Zaloguj się ponownie.")
		return
	}
	writeJSON(w, 200, map[string]any{"login": u.Login, "created": u.Created})
}

func (sv *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	sv.st.mu.Lock()
	n := len(sv.st.users)
	sv.st.mu.Unlock()
	ai := false
	c := http.Client{Timeout: 2 * time.Second}
	if res, err := c.Get(*ollamaURL + "/api/version"); err == nil {
		ai = res.StatusCode == 200
		res.Body.Close()
	}
	writeJSON(w, 200, map[string]any{"ok": true, "app": "passa", "version": version, "accounts": n, "maxAccounts": *maxUsers, "ai": ai})
}

/* --- AI: pośrednik do Ollamy, tylko dla zalogowanych --- */

func (sv *Server) handleAITags(w http.ResponseWriter, r *http.Request) {
	if u, _ := sv.auth(r); u == nil {
		fail(w, 401, "Zaloguj się ponownie.")
		return
	}
	c := http.Client{Timeout: 5 * time.Second}
	res, err := c.Get(*ollamaURL + "/api/tags")
	if err != nil {
		fail(w, 503, "Ollama na Macu nie działa.")
		return
	}
	defer res.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.StatusCode)
	io.Copy(w, res.Body)
}

func (sv *Server) handleAIChat(w http.ResponseWriter, r *http.Request) {
	u, _ := sv.auth(r)
	if u == nil {
		fail(w, 401, "Zaloguj się ponownie.")
		return
	}
	if sv.aiHits.count(u.ID, time.Hour) >= 60 {
		fail(w, 429, "Limit wiadomości do AI na tę godzinę wyczerpany.")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || len(body) == 0 || body[0] != '{' {
		fail(w, 400, "Złe dane.")
		return
	}
	sv.aiHits.add(u.ID)
	// jedno zapytanie naraz, żeby Mac się nie zadławił
	select {
	case sv.aiSem <- struct{}{}:
	case <-time.After(5 * time.Minute):
		fail(w, 503, "AI jest zajęte. Spróbuj za chwilę.")
		return
	case <-r.Context().Done():
		return
	}
	defer func() { <-sv.aiSem }()
	req, _ := http.NewRequestWithContext(r.Context(), "POST", *ollamaURL+"/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fail(w, 503, "Ollama na Macu nie działa.")
		return
	}
	defer res.Body.Close()
	w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(res.StatusCode)
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

/* ================= kopie zapasowe ================= */

func backupLoop(st *Store) {
	for {
		day := time.Now().Format("2006-01-02")
		dst := filepath.Join(st.dir, "backups", day)
		if _, err := os.Stat(dst); errors.Is(err, os.ErrNotExist) {
			if err := copyDir(st.dir, dst); err != nil {
				log.Printf("kopia zapasowa: %v", err)
			} else {
				log.Printf("kopia zapasowa: %s", dst)
			}
			pruneBackups(filepath.Join(st.dir, "backups"), 14)
		}
		time.Sleep(time.Hour)
	}
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(filepath.Join(dst, "users"), 0700); err != nil {
		return err
	}
	for _, f := range []string{"accounts.json", "config.json"} {
		copyFile(filepath.Join(src, f), filepath.Join(dst, f))
	}
	ents, _ := os.ReadDir(filepath.Join(src, "users"))
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".json") {
			if err := copyFile(filepath.Join(src, "users", e.Name()), filepath.Join(dst, "users", e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(a, b string) error {
	d, err := os.ReadFile(a)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return os.WriteFile(b, d, 0600)
}

func pruneBackups(dir string, keep int) {
	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		os.RemoveAll(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}

/* ================= polecenia z Terminala ================= */

func restartService() {
	if runtime.GOOS != "darwin" {
		return
	}
	if err := exec.Command("launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/pl.passa.server", os.Getuid())).Run(); err == nil {
		fmt.Println("Serwer uruchomiony ponownie.")
	}
}

func cli(args []string) bool {
	if len(args) == 0 || args[0] == "serve" {
		return false
	}
	st, err := openStore(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	switch args[0] {
	case "invite":
		fmt.Println("Kod zaproszenia:", st.cfg.Invite)
	case "new-invite":
		st.cfg.Invite = newInvite()
		writeJSONFile(filepath.Join(st.dir, "config.json"), st.cfg)
		fmt.Println("Nowy kod zaproszenia:", st.cfg.Invite)
		restartService()
	case "users":
		fmt.Printf("Konta: %d / %d\n", len(st.users), *maxUsers)
		us := make([]*User, 0)
		for _, u := range st.users {
			us = append(us, u)
		}
		sort.Slice(us, func(i, j int) bool { return us[i].Created.Before(us[j].Created) })
		for _, u := range us {
			fmt.Printf("  %-20s założone %s, ostatnio %s\n", u.Login, u.Created.Format("2006-01-02"), u.LastSeen.Format("2006-01-02 15:04"))
		}
	case "reset-password":
		if len(args) < 2 {
			fmt.Println("Użycie: passa-server reset-password LOGIN")
			os.Exit(1)
		}
		u := st.users[strings.ToLower(args[1])]
		if u == nil {
			fmt.Println("Nie ma takiego konta.")
			os.Exit(1)
		}
		pw := randToken(9)
		salt := make([]byte, 16)
		rand.Read(salt)
		u.Salt, u.Iter = hex.EncodeToString(salt), pbkdfIter
		u.Hash = hashPassword(pw, salt, u.Iter)
		for k, s := range st.sessions {
			if s.UserID == u.ID {
				delete(st.sessions, k)
			}
		}
		st.saveUsersLocked()
		st.saveSessionsLocked()
		fmt.Println("Nowe hasło dla", u.Login+":", pw)
		restartService()
	case "delete-user":
		if len(args) < 2 {
			fmt.Println("Użycie: passa-server delete-user LOGIN")
			os.Exit(1)
		}
		u := st.users[strings.ToLower(args[1])]
		if u == nil {
			fmt.Println("Nie ma takiego konta.")
			os.Exit(1)
		}
		fmt.Printf("Usunąć konto %s razem z danymi? Wpisz TAK: ", u.Login)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(line) != "TAK" {
			fmt.Println("Anulowano.")
			return true
		}
		delete(st.users, u.Login)
		for k, s := range st.sessions {
			if s.UserID == u.ID {
				delete(st.sessions, k)
			}
		}
		os.Remove(st.dataPath(u.ID))
		st.saveUsersLocked()
		st.saveSessionsLocked()
		fmt.Println("Usunięto konto.")
		restartService()
	case "version":
		fmt.Println("passa-server", version)
	default:
		fmt.Println("Polecenia: serve | invite | new-invite | users | reset-password LOGIN | delete-user LOGIN | version")
		os.Exit(1)
	}
	return true
}

func main() {
	flag.Parse()
	if cli(flag.Args()) {
		return
	}
	st, err := openStore(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	sv := &Server{st: st, fails: newLimiter(), regs: newLimiter(), aiHits: newLimiter(), aiSem: make(chan struct{}, 1), origins: map[string]bool{}}
	for _, o := range strings.Split(*origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			sv.origins[o] = true
		}
	}
	go backupLoop(st)

	mux := http.NewServeMux()
	post := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				fail(w, 405, "Zła metoda.")
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/api/health", sv.handleHealth)
	mux.HandleFunc("/api/register", post(sv.handleRegister))
	mux.HandleFunc("/api/login", post(sv.handleLogin))
	mux.HandleFunc("/api/logout", post(sv.handleLogout))
	mux.HandleFunc("/api/password", post(sv.handlePassword))
	mux.HandleFunc("/api/me", sv.handleMe)
	mux.HandleFunc("/api/sync", sv.handleSync)
	mux.HandleFunc("/api/ai/tags", sv.handleAITags)
	mux.HandleFunc("/api/ai/chat", post(sv.handleAIChat))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Serwer Passa</title><body style="font:16px system-ui;background:#0E0F14;color:#F3F1EA;padding:24px">Serwer Passa działa. <a style="color:#FFB23E" href="https://rozwalka.github.io/Passa/">Otwórz apkę</a></body>`)
			return
		}
		fail(w, 404, "Nie ma takiej strony.")
	})

	srv := &http.Server{Addr: *addr, Handler: sv.cors(mux), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	log.Printf("Passa server %s na %s, dane: %s, konta: %d/%d", version, *addr, st.dir, len(st.users), *maxUsers)
	log.Fatal(srv.ListenAndServe())
}
