package httpapi

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"muriscore/panel/internal/rules"
	"muriscore/panel/internal/store"
)

// passwordAlphabet has 32 characters, so a random byte maps to one with no
// bias; the letters that look alike are left out because these are read off a
// screen and typed by hand.
const passwordAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"

// generatePassword returns a password of n characters from crypto/rand.
func generatePassword(n int) string {
	var builder strings.Builder

	builder.Grow(n)

	max := big.NewInt(int64(len(passwordAlphabet)))

	for i := 0; i < n; i++ {
		index, err := rand.Int(rand.Reader, max)
		if err != nil {
			// crypto/rand does not fail on Linux; if it does, an empty
			// password must never be stored, so this is loud.
			panic("no randomness: " + err.Error())
		}

		builder.WriteByte(passwordAlphabet[index.Int64()])
	}

	return builder.String()
}

// queued answers a call that has only put work on the queue.
func queued(w http.ResponseWriter, action string, extra map[string]any) {
	body := map[string]any{"queued": true, "action": action}

	for key, value := range extra {
		body[key] = value
	}

	writeJSON(w, http.StatusAccepted, body)
}

// caller returns the signed-in panel user.
func (s *Server) caller(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	user, ok := userFrom(r.Context())
	if !ok {
		fail(w, http.StatusUnauthorized, codeUnauthorized, "Sign in to continue.")

		return store.User{}, false
	}

	return user, true
}

// mayManage reports whether caller may act on account. A reseller may only
// manage the accounts it owns, which is the one rule that keeps resellers
// apart.
func mayManage(caller, account store.User) bool {
	if caller.IsAdmin() {
		return true
	}

	return account.OwnerID != nil && *account.OwnerID == caller.ID
}

// visibleAccount loads a hosting account by username and checks that the
// caller may manage it. Anything it may not see is answered as missing, so the
// panel does not confirm which usernames exist.
func (s *Server) visibleAccount(w http.ResponseWriter, r *http.Request, username string) (store.User, bool) {
	caller, ok := s.caller(w, r)
	if !ok {
		return store.User{}, false
	}

	account, err := s.store.UserByUsername(r.Context(), rules.NormalizeUsername(username))
	if err != nil || account.Role != store.RoleUser || !mayManage(caller, account) {
		fail(w, http.StatusNotFound, codeNotFound, "There is no account by that name.")

		return store.User{}, false
	}

	return account, true
}

// visibleOwner returns the account for a username that a module row refers to.
func (s *Server) visibleOwner(w http.ResponseWriter, r *http.Request, username string) (store.User, bool) {
	return s.visibleAccount(w, r, username)
}

// accountInPath is visibleAccount for routes shaped /accounts/{username}/… .
func (s *Server) accountInPath(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	return s.visibleAccount(w, r, r.PathValue("username"))
}

var recordNamePattern = regexp.MustCompile(`^(@|\*|[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?)?$`)

// validRecordName accepts a relative name inside a zone, including "@" and "*".
func validRecordName(name string) bool {
	return len(name) <= 253 && recordNamePattern.MatchString(name)
}

// dnsTypes are the record types the panel can write. The list is short on
// purpose: every type here is one the agent's zone writer understands.
var dnsTypes = map[string]bool{
	"A": true, "AAAA": true, "CNAME": true, "MX": true, "TXT": true,
	"NS": true, "SRV": true, "CAA": true,
}

// cronSchedulePattern is a five-field crontab line: numbers, ranges, lists,
// steps and the usual words.
var cronSchedulePattern = regexp.MustCompile(
	`^(@(reboot|yearly|annually|monthly|weekly|daily|midnight|hourly)|` +
		`([0-9*,/-]+)\s+([0-9*,/-]+)\s+([0-9*,/-]+)\s+([0-9*,/-]+)\s+([0-9*,/-]+))$`)

// localPartPattern is what may stand before the @ in a mailbox or forwarder.
var localPartPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,62}[a-z0-9])?$`)

// validIPv4 reports whether a value is a plain IPv4 address.
func validIPv4(value string) bool {
	address := net.ParseIP(value)

	return address != nil && address.To4() != nil
}

// validIPv6 reports whether a value is an IPv6 address.
func validIPv6(value string) bool {
	address := net.ParseIP(value)

	return address != nil && address.To4() == nil
}

// parseID reads a numeric path value.
func parseID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		fail(w, http.StatusNotFound, codeNotFound, "There is nothing here by that id.")

		return 0, false
	}

	return id, true
}

// visibleAccountByID loads an account by row id and checks the caller may see
// it. A record owned by somebody else answers 404 rather than 403, exactly like
// a name that does not exist.
func (s *Server) visibleAccountByID(w http.ResponseWriter, r *http.Request, id int64) (store.User, bool) {
	caller, ok := s.caller(w, r)
	if !ok {
		return store.User{}, false
	}

	account, err := s.store.UserByID(r.Context(), id)
	if err != nil || account.Role != store.RoleUser || !mayManage(caller, account) {
		fail(w, http.StatusNotFound, codeNotFound, "There is no account here.")

		return store.User{}, false
	}

	return account, true
}

// audit records what an operator did, with the request's origin.
func (s *Server) audit(r *http.Request, caller store.User, action, subjectType, subjectID string, extra map[string]any) {
	actorID := caller.ID

	_ = s.store.Audit(r.Context(), &actorID, action, subjectType, subjectID, s.clientIP(r), userAgent(r), extra)
}

// domainedAccount loads the account that owns a domain, checking permission.
func (s *Server) domainedAccount(w http.ResponseWriter, r *http.Request, domain store.Domain) (store.User, bool) {
	account, err := s.store.UserByID(r.Context(), domain.UserID)
	if err != nil {
		s.failStore(w, r, err)

		return store.User{}, false
	}

	caller, ok := s.caller(w, r)
	if !ok {
		return store.User{}, false
	}

	if !mayManage(caller, account) {
		fail(w, http.StatusNotFound, codeNotFound, "There is no website by that id.")

		return store.User{}, false
	}

	return account, true
}

// ownedDomain loads a domain by path id and checks the caller may manage it.
func (s *Server) ownedDomain(w http.ResponseWriter, r *http.Request) (store.Domain, store.User, bool) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return store.Domain{}, store.User{}, false
	}

	domain, err := s.store.DomainByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, http.StatusNotFound, codeNotFound, "There is no website by that id.")
		} else {
			s.failStore(w, r, err)
		}

		return store.Domain{}, store.User{}, false
	}

	account, ok := s.domainedAccount(w, r, domain)
	if !ok {
		return store.Domain{}, store.User{}, false
	}

	return domain, account, true
}

// queueFor queues an agent call for a hosting account and returns the task's
// id, so the screen can follow it in the activity list.
func (s *Server) queueFor(r *http.Request, caller, account store.User, action string, params map[string]any) (string, error) {
	payload, err := json.Marshal(params)
	if err != nil {
		return "", err
	}

	taskID := newID()
	accountID := account.ID
	callerID := caller.ID

	if err := s.store.CreateTask(r.Context(), taskID, &accountID, &callerID, action, string(payload)); err != nil {
		return "", err
	}

	return taskID, nil
}
