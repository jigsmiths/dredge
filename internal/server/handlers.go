package server

import (
	"encoding/json"
	"net/http"
)

func healthHandler(version string) http.HandlerFunc {
	body, _ := json.Marshal(map[string]string{"status": "ok", "version": version})
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}
