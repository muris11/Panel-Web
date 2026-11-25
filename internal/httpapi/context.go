package httpapi

import (
	"context"
	"net/http"
	"time"
)

// timeLimited bounds a call by the request's lifetime and by a ceiling,
// whichever ends first. It is used where the caller waits on something that
// might not answer at all.
func timeLimited(r *http.Request, ceiling time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), ceiling)
}
