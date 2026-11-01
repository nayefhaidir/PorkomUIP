package application

import (
	"context"
	"database/sql"
)

type Storage interface {
	DB() *sql.DB
	Driver() string
	Q(string) string
	Exec(context.Context, string, ...any) (sql.Result, error)
	WithTx(context.Context, func(*sql.Tx) error) error
}
