package repo

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound = errors.New("repo: not found")
	ErrConflict = errors.New("repo: conflict")
)

func mapDBError(err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23503", "23514":
			return fmt.Errorf("%w: %s", ErrConflict, pgErr.Message)
		}
	}

	return err
}
