package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"rayxo/panel/internal/store"
)

// redisMemoryChoices are the ceilings the drawer offers. The agent takes any
// number, but a short list keeps an operator from typing 999999 by accident.
var redisMemoryChoices = []int{64, 128, 256, 512, 1024}

// handleRedis reports and creates an account's cache instance.
//
// A panel user owns one instance at most. Asking for it twice is a change, not
// a second cache, which is why the drawer replaces the password instead of
// adding a row.
func (s *Server) handleRedis(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		instance, err := s.redisFor(w, r, caller)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		answer := map[string]any{"instance": nil, "choices": redisMemoryChoices}

		if instance != nil {
			answer["instance"] = instance
		}

		writeJSON(w, http.StatusOK, answer)

	case http.MethodPost:
		var request struct {
			Username    string `json:"username"`
			MaxMemoryMB int    `json:"maxMemoryMb"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		account, ok := s.redisAccount(w, r, request.Username)
		if !ok {
			return
		}

		memory, ok := s.redisMemory(w, r, request.MaxMemoryMB)
		if !ok {
			return
		}

		password := generatePassword(20)

		stored, err := s.encodeSecret(password)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		instance, err := s.store.SaveRedisInstance(r.Context(), account.ID, stored, memory)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		if _, err := s.queueFor(r, caller, account, "redis.provision", map[string]any{
			"username":      account.Username,
			"password":      password,
			"max_memory_mb": memory,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "redis.provisioned", "redis_instance", strconv.FormatInt(instance.ID, 10), map[string]any{
			"username":      account.Username,
			"max_memory_mb": memory,
		})

		writeJSON(w, http.StatusCreated, map[string]any{
			"instance": s.describeRedis(instance),
			"password": password,
		})
	}
}

// handleRedisAction rotates the password, turns the instance off, or forgets it.
func (s *Server) handleRedisAction(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodPost:
		instance, account, ok := s.redisTarget(w, r)
		if !ok {
			return
		}

		// POST without a suffix means "rotate": the old password stops working.
		password := generatePassword(20)

		stored, err := s.encodeSecret(password)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		updated, err := s.store.SaveRedisInstance(r.Context(), account.ID, stored, instance.MaxMemoryMB)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		if _, err := s.queueFor(r, caller, account, "redis.provision", map[string]any{
			"username":      account.Username,
			"password":      password,
			"max_memory_mb": instance.MaxMemoryMB,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "redis.password_rotated", "redis_instance", strconv.FormatInt(instance.ID, 10),
			map[string]any{"username": account.Username})

		writeJSON(w, http.StatusOK, map[string]any{
			"instance": s.describeRedis(updated),
			"password": password,
		})

	case http.MethodPatch:
		instance, account, ok := s.redisTarget(w, r)
		if !ok {
			return
		}

		var request struct {
			MaxMemoryMB int `json:"maxMemoryMb"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		memory, ok := s.redisMemory(w, r, request.MaxMemoryMB)
		if !ok {
			return
		}

		stored, err := s.store.RedisPassword(r.Context(), instance.ID)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		password, err := s.decodeSecret(stored)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		updated, err := s.store.SaveRedisInstance(r.Context(), account.ID, stored, memory)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		if _, err := s.queueFor(r, caller, account, "redis.provision", map[string]any{
			"username":      account.Username,
			"password":      password,
			"max_memory_mb": memory,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "redis.resized", "redis_instance", strconv.FormatInt(instance.ID, 10),
			map[string]any{"max_memory_mb": memory})

		writeJSON(w, http.StatusOK, map[string]any{"instance": s.describeRedis(updated)})

	case http.MethodDelete:
		instance, account, ok := s.redisTarget(w, r)
		if !ok {
			return
		}

		if _, err := s.queueFor(r, caller, account, "redis.deprovision", map[string]any{
			"username": account.Username,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		if err := s.store.DeleteRedisInstance(r.Context(), instance.ID); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "redis.removed", "redis_instance", strconv.FormatInt(instance.ID, 10),
			map[string]any{"username": account.Username})

		w.WriteHeader(http.StatusNoContent)
	}
}

// redisTarget loads the instance named in the path, with the account that owns
// it, after checking the caller may touch that account.
func (s *Server) redisTarget(w http.ResponseWriter, r *http.Request) (store.RedisInstance, store.User, bool) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return store.RedisInstance{}, store.User{}, false
	}

	instance, err := s.store.RedisInstanceByID(r.Context(), id)
	if err != nil {
		s.failStore(w, r, err)

		return store.RedisInstance{}, store.User{}, false
	}

	account, ok := s.visibleAccountByID(w, r, instance.UserID)
	if !ok {
		return store.RedisInstance{}, store.User{}, false
	}

	return instance, account, true
}

// redisFor returns the caller's own instance: an administrator has none of
// their own, so the answer is nil rather than an error.
func (s *Server) redisFor(w http.ResponseWriter, r *http.Request, caller store.User) (*store.RedisInstance, error) {
	if caller.Role != store.RoleUser {
		return nil, nil
	}

	instance, err := s.store.RedisInstanceFor(r.Context(), caller.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	described := s.describeRedis(instance)

	return &described, nil
}

// redisAccount picks the account an instance would belong to: their own for a
// hosting user, or the named one for an operator.
func (s *Server) redisAccount(w http.ResponseWriter, r *http.Request, username string) (store.User, bool) {
	caller, ok := s.caller(w, r)
	if !ok {
		return store.User{}, false
	}

	if caller.Role == store.RoleUser {
		return caller, true
	}

	return s.visibleAccount(w, r, username)
}

// redisMemory checks the requested ceiling.
func (s *Server) redisMemory(w http.ResponseWriter, r *http.Request, value int) (int, bool) {
	if value == 0 {
		return 256, true
	}

	for _, choice := range redisMemoryChoices {
		if choice == value {
			return value, true
		}
	}

	failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That memory ceiling cannot be used.", map[string]string{
		"maxMemoryMb": "Pick one of 64, 128, 256, 512 or 1024 MB.",
	})

	return 0, false
}

// describeRedis adds the address a client would connect to. It is derived, not
// stored: one instance per account lives on the panel's own host.
func (s *Server) describeRedis(instance store.RedisInstance) store.RedisInstance {
	instance.Host = strings.TrimSpace(s.cfg.PublicIP)
	if instance.Host == "" {
		instance.Host = "127.0.0.1"
	}

	instance.Port = 6379

	return instance
}
