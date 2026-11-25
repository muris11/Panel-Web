package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"muriscore/panel/internal/store"
)

// fileCall runs one fs.* action for an account and passes the agent's answer
// straight through. The file manager is interactive, so these calls wait on
// the agent instead of going through the queue.
func (s *Server) fileCall(w http.ResponseWriter, r *http.Request, account store.User, action string, params map[string]any) {
	params["username"] = account.Username

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	answer, err := s.agent.Call(ctx, action, params)
	if err != nil {
		s.failAgent(w, r, err)

		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if len(answer) == 0 {
		answer = []byte(`{}`)
	}

	_, _ = w.Write(answer)
}

// fileTarget resolves the account named in a query string or a body.
func (s *Server) fileTarget(w http.ResponseWriter, r *http.Request, username string) (store.User, bool) {
	return s.visibleAccount(w, r, username)
}

// handleFiles lists a folder or one file's contents.
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	account, ok := s.fileTarget(w, r, query.Get("username"))
	if !ok {
		return
	}

	path := query.Get("path")

	if query.Get("read") != "" {
		s.fileCall(w, r, account, "fs.read", map[string]any{"path": path})

		return
	}

	s.fileCall(w, r, account, "fs.list", map[string]any{"path": path})
}

// handleFileSearch finds files by name under a folder.
func (s *Server) handleFileSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	account, ok := s.fileTarget(w, r, query.Get("username"))
	if !ok {
		return
	}

	needle := strings.TrimSpace(query.Get("query"))
	if needle == "" {
		fail(w, http.StatusUnprocessableEntity, codeInvalid, "Type something to search for.")

		return
	}

	s.fileCall(w, r, account, "fs.search", map[string]any{
		"path":  query.Get("path"),
		"query": needle,
	})
}

// handleFileAction is one entry point for the write operations, because they
// differ only in which agent action they call.
func (s *Server) handleFileAction(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username    string   `json:"username"`
		Path        string   `json:"path"`
		Paths       []string `json:"paths"`
		Name        string   `json:"name"`
		Content     string   `json:"content"`
		Version     string   `json:"version"`
		Destination string   `json:"destination"`
		Format      string   `json:"format"`
		Overwrite   bool     `json:"overwrite"`
		Mode        uint32   `json:"mode"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	account, ok := s.fileTarget(w, r, request.Username)
	if !ok {
		return
	}

	action := r.PathValue("action")

	switch action {
	case "write":
		if request.Path == "" {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "A file needs a name.", map[string]string{
				"path": "Choose a file to save.",
			})

			return
		}

		s.fileCall(w, r, account, "fs.write", map[string]any{
			"path":    request.Path,
			"content": request.Content,
			"version": request.Version,
		})

	case "mkdir":
		if request.Path == "" {
			fail(w, http.StatusUnprocessableEntity, codeInvalid, "A folder needs a name.")

			return
		}

		s.fileCall(w, r, account, "fs.mkdir", map[string]any{"path": request.Path})

	case "rename":
		if request.Path == "" || request.Name == "" {
			fail(w, http.StatusUnprocessableEntity, codeInvalid, "A rename needs the file and its new name.")

			return
		}

		s.fileCall(w, r, account, "fs.rename", map[string]any{
			"path": request.Path,
			"name": request.Name,
		})

	case "delete":
		if len(request.Paths) == 0 {
			fail(w, http.StatusUnprocessableEntity, codeInvalid, "Choose something to delete.")

			return
		}

		s.fileCall(w, r, account, "fs.delete", map[string]any{"paths": request.Paths})

	case "chmod":
		if len(request.Paths) == 0 {
			fail(w, http.StatusUnprocessableEntity, codeInvalid, "Choose something to change.")

			return
		}

		if request.Mode == 0 || request.Mode > 0o777 {
			fail(w, http.StatusUnprocessableEntity, codeInvalid, "A mode is three octal digits, e.g. 644.")

			return
		}

		s.fileCall(w, r, account, "fs.chmod", map[string]any{
			"paths": request.Paths,
			"mode":  request.Mode,
		})

	case "compress":
		if len(request.Paths) == 0 || request.Destination == "" {
			fail(w, http.StatusUnprocessableEntity, codeInvalid, "Choose what to compress and where to put it.")

			return
		}

		format := request.Format
		if format == "" {
			format = "zip"
		}

		s.fileCall(w, r, account, "fs.compress", map[string]any{
			"paths":       request.Paths,
			"destination": request.Destination,
			"format":      format,
		})

	case "extract":
		if request.Path == "" {
			fail(w, http.StatusUnprocessableEntity, codeInvalid, "Choose an archive to extract.")

			return
		}

		s.fileCall(w, r, account, "fs.extract", map[string]any{
			"path":        request.Path,
			"destination": request.Destination,
			"overwrite":   request.Overwrite,
		})

	default:
		fail(w, http.StatusNotFound, codeNotFound, "There is no file action by that name.")
	}
}

// failAgent turns an agent refusal into the panel's error shape.
func (s *Server) failAgent(w http.ResponseWriter, r *http.Request, err error) {
	message := err.Error()

	writeJSON(w, http.StatusBadGateway, map[string]any{
		"error": map[string]any{
			"code":    "agent_error",
			"message": message,
		},
	})
}

// decodeAgentAnswer reads an agent answer into a panel type.
func decodeAgentAnswer(raw json.RawMessage, dest any) error {
	if len(raw) == 0 {
		return nil
	}

	return json.Unmarshal(raw, dest)
}
