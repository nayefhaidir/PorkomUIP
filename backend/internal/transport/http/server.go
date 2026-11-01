package httpapi

import (
	"net/http"
	"sync"
	"time"

	"uip-meeting-backend/internal/application"
)

type Storage = application.Storage

type Server struct {
	store       Storage
	loginMu     sync.Mutex
	loginFailed map[string]loginFailure
}

type loginFailure struct {
	count   int
	resetAt time.Time
}

func NewHandler(storage Storage) http.Handler {
	server := &Server{store: storage}
	return requestLog(server.auth(server.routes()))
}
