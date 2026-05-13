package api

import (
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

func NewMiddleware(middlewareName string) func(next http.Handler) http.Handler {
	switch middlewareName {
	case "logger":
		return middleware.Logger
	case "recoverer":
		return middleware.Recoverer
	case "requestid":
		return middleware.RequestID
	case "realip":
		return middleware.RealIP
	case "compress":
		return middleware.Compress(5, "text/json", "text/plain", "application/json")
	case "timeout":
		return middleware.Timeout(30 * 1000000000) // 30 seconds
	default:
		return func(next http.Handler) http.Handler {
			return next
		}
	}
}

func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func RateLimit(next http.Handler) http.Handler {
	return next
}